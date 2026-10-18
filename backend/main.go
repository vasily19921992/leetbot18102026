package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const trialLength = 24 * time.Hour
const monthLength = 30 * 24 * time.Hour
const monthlyPrice = 200

type User struct {
	Email             string    "json:\"email\""
	PasswordHash      string    "json:\"passwordHash\""
	CreatedAt         time.Time "json:\"createdAt\""
	TrialEndsAt       time.Time "json:\"trialEndsAt\""
	PaidUntil         time.Time "json:\"paidUntil\""
	EmailVerified     bool      "json:\"emailVerified\""
	VerificationCode  string    "json:\"verificationCode\""
	VerificationUntil time.Time "json:\"verificationUntil\""
	Completed         []int     "json:\"completed\""
	FocusSeconds      int       "json:\"focusSeconds\""
	TelegramID        int64     "json:\"telegramId,omitempty\""
	TelegramUsername  string    "json:\"telegramUsername,omitempty\""
	TelegramName      string    "json:\"telegramName,omitempty\""
}
type Payment struct {
	Label       string    "json:\"label\""
	Email       string    "json:\"email\""
	UserID      string    "json:\"userId,omitempty\""
	Amount      int       "json:\"amount\""
	Currency    string    "json:\"currency,omitempty\""
	Status      string    "json:\"status\""
	CreatedAt   time.Time "json:\"createdAt\""
	OperationID string    "json:\"operationId,omitempty\""
	ChargeID    string    "json:\"chargeId,omitempty\""
}
type diskState struct {
	Users     map[string]*User    "json:\"users\""
	Payments  map[string]*Payment "json:\"payments\""
	Processed map[string]bool     "json:\"processed\""
}
type Store struct {
	sync.RWMutex
	users     map[string]*User
	payments  map[string]*Payment
	processed map[string]bool
	path      string
}
type Exercise struct {
	ID            int      "json:\"id\""
	Title         string   "json:\"title\""
	Category      string   "json:\"category\""
	Level         int      "json:\"level\""
	LevelName     string   "json:\"levelName\""
	Text          string   "json:\"text\""
	Question      string   "json:\"question\""
	Options       []string "json:\"options\""
	CorrectOption int      "json:\"correctOption\""
}
type TextTemplate struct {
	Category string
	Title    string
	Lines    []string
}
type Server struct {
	store                 *Store
	sessionSecret         []byte
	telegramBotToken      string
	telegramWebhookSecret string
	telegramHTTPClient    *http.Client
}

func main() {
	loadDotEnv(".env")
	path := env("DATA_FILE", filepath.Join("backend", "data", "app.json"))
	store, err := loadStore(path)
	if err != nil {
		log.Fatal(err)
	}
	secret := os.Getenv("SESSION_SECRET")
	if env("APP_ENV", "development") == "production" && (len(secret) < 32 || secret == "replace-with-a-long-random-secret") {
		log.Fatal("SESSION_SECRET is required in production")
	}
	if secret == "" {
		secret = "local-only-change-this-session-secret"
		log.Println("SESSION_SECRET is unset; using a development-only key")
	}
	s := &Server{
		store:                 store,
		sessionSecret:         []byte(secret),
		telegramBotToken:      strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		telegramWebhookSecret: strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET")),
		telegramHTTPClient:    &http.Client{Timeout: 10 * time.Second},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/exercises", s.exercises)
	mux.HandleFunc("POST /api/register", s.register)
	mux.HandleFunc("POST /api/verify-email", s.verifyEmail)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("POST /api/telegram/auth", s.telegramAuth)
	mux.HandleFunc("POST /api/telegram/webhook", s.telegramWebhook)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("POST /api/progress", s.progress)
	mux.HandleFunc("POST /api/payment/start", s.startPayment)
	mux.HandleFunc("GET /api/payment/status", s.paymentStatus)
	mux.Handle("/", s.frontend())
	addr := env("LISTEN_ADDR", ":8080")
	log.Printf("Летри запущен на %s", addr)
	log.Fatal(http.ListenAndServe(addr, secureHeaders(mux)))
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, map[string]any{"ok": true, "exercises": 520})
}
func (s *Server) exercises(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, generateExercises())
}

type telegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

func validateTelegramInitData(initData, botToken string, now time.Time) (telegramUser, error) {
	var user telegramUser
	values, err := url.ParseQuery(initData)
	if err != nil || initData == "" {
		return user, errors.New("invalid init data")
	}
	for _, entries := range values {
		if len(entries) != 1 {
			return user, errors.New("duplicate init data parameter")
		}
	}
	hash := values.Get("hash")
	providedHash, err := hex.DecodeString(hash)
	if err != nil || len(providedHash) != sha256.Size {
		return user, errors.New("invalid init data hash")
	}
	authDate, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || authDate > now.Add(time.Minute).Unix() || now.Unix()-authDate > int64((24*time.Hour)/time.Second) {
		return user, errors.New("init data is expired")
	}
	keys := make([]string, 0, len(values)-1)
	for key := range values {
		if key != "hash" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+values.Get(key))
	}
	secretMAC := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretMAC.Write([]byte(botToken))
	checkMAC := hmac.New(sha256.New, secretMAC.Sum(nil))
	_, _ = checkMAC.Write([]byte(strings.Join(lines, "\n")))
	if !hmac.Equal(providedHash, checkMAC.Sum(nil)) {
		return user, errors.New("init data signature does not match")
	}
	if err := json.Unmarshal([]byte(values.Get("user")), &user); err != nil || user.ID < 1 {
		return user, errors.New("invalid Telegram user")
	}
	return user, nil
}

func (s *Server) telegramAuth(w http.ResponseWriter, r *http.Request) {
	if s.telegramBotToken == "" {
		jsonError(w, http.StatusServiceUnavailable, "Telegram Mini App не настроен.")
		return
	}
	var in struct {
		InitData string `json:"initData"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	tgUser, err := validateTelegramInitData(in.InitData, s.telegramBotToken, time.Now().UTC())
	if err != nil {
		jsonError(w, http.StatusUnauthorized, "Не удалось проверить данные Telegram. Открой приложение из Telegram.")
		return
	}
	userID := "telegram:" + strconv.FormatInt(tgUser.ID, 10)
	name := strings.TrimSpace(strings.TrimSpace(tgUser.FirstName) + " " + strings.TrimSpace(tgUser.LastName))
	if name == "" {
		name = tgUser.Username
	}
	s.store.Lock()
	user := s.store.users[userID]
	if user == nil {
		now := time.Now().UTC()
		user = &User{CreatedAt: now, TrialEndsAt: now.Add(trialLength), Completed: []int{}}
		s.store.users[userID] = user
	}
	user.TelegramID = tgUser.ID
	user.TelegramUsername = tgUser.Username
	user.TelegramName = name
	if err := s.store.saveLocked(); err != nil {
		s.store.Unlock()
		jsonError(w, http.StatusInternalServerError, "Не удалось сохранить Telegram-аккаунт.")
		return
	}
	public := s.publicUser(user, time.Now().UTC())
	s.store.Unlock()
	s.setSession(w, userID)
	jsonOut(w, http.StatusOK, map[string]any{"user": public})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string "json:\"email\""
		Password string "json:\"password\""
	}
	if !readJSON(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(email) > 254 {
		jsonError(w, 400, "Введите корректный адрес почты.")
		return
	}
	if len(in.Password) < 8 || len(in.Password) > 200 {
		jsonError(w, 400, "Пароль должен содержать от 8 до 200 символов.")
		return
	}
	if env("APP_ENV", "development") == "production" && !smtpReady() {
		jsonError(w, 503, "Для production обязательна настройка SMTP-подтверждения.")
		return
	}
	if smtpPartial() {
		jsonError(w, 503, "Заполни все параметры SMTP в .env.")
		return
	}
	pass, err := hashPassword(in.Password)
	if err != nil {
		jsonError(w, 500, "Не удалось создать аккаунт.")
		return
	}
	now := time.Now().UTC()
	user := &User{Email: email, PasswordHash: pass, CreatedAt: now, TrialEndsAt: now.Add(trialLength), Completed: []int{}}
	code := ""
	if smtpReady() {
		code, err = randomDigits(6)
		if err != nil {
			jsonError(w, 500, "Не удалось создать код.")
			return
		}
		user.VerificationCode = code
		user.VerificationUntil = now.Add(30 * time.Minute)
	} else {
		user.EmailVerified = true
	}
	s.store.Lock()
	if s.store.users[email] != nil {
		s.store.Unlock()
		jsonError(w, 409, "Аккаунт с этой почтой уже существует.")
		return
	}
	s.store.users[email] = user
	if err = s.store.saveLocked(); err != nil {
		delete(s.store.users, email)
		s.store.Unlock()
		jsonError(w, 500, "Не удалось сохранить аккаунт.")
		return
	}
	s.store.Unlock()
	if smtpReady() {
		if err = sendVerification(email, code); err != nil {
			s.store.Lock()
			delete(s.store.users, email)
			_ = s.store.saveLocked()
			s.store.Unlock()
			jsonError(w, 502, "Не удалось отправить письмо. Проверь SMTP.")
			return
		}
		jsonOut(w, 201, map[string]any{"needsVerification": true, "email": email})
		return
	}
	s.setSession(w, email)
	jsonOut(w, 201, map[string]any{"needsVerification": false, "user": s.publicUser(user, time.Now().UTC()), "note": "SMTP не настроен: локальный режим подтверждает почту автоматически."})
}
func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string "json:\"email\""
		Code  string "json:\"code\""
	}
	if !readJSON(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	s.store.Lock()
	defer s.store.Unlock()
	u := s.store.users[email]
	if u == nil || u.VerificationCode == "" || time.Now().After(u.VerificationUntil) {
		jsonError(w, 400, "Код не найден или истёк. Зарегистрируйся заново.")
		return
	}
	if !hmac.Equal([]byte(u.VerificationCode), []byte(strings.TrimSpace(in.Code))) {
		jsonError(w, 400, "Код не подошёл. Проверь письмо.")
		return
	}
	u.EmailVerified = true
	u.VerificationCode = ""
	u.VerificationUntil = time.Time{}
	if err := s.store.saveLocked(); err != nil {
		jsonError(w, 500, "Не удалось сохранить подтверждение.")
		return
	}
	s.setSession(w, email)
	jsonOut(w, 200, map[string]any{"user": s.publicUser(u, time.Now().UTC())})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string "json:\"email\""
		Password string "json:\"password\""
	}
	if !readJSON(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	s.store.RLock()
	u := s.store.users[email]
	pass := ""
	verified := false
	if u != nil {
		pass = u.PasswordHash
		verified = u.EmailVerified
	}
	s.store.RUnlock()
	if !verifyPassword(in.Password, pass) {
		jsonError(w, 401, "Почта или пароль не подошли.")
		return
	}
	if !verified {
		jsonError(w, 403, "Сначала подтверди почту по коду из письма.")
		return
	}
	s.setSession(w, email)
	s.store.RLock()
	u = s.store.users[email]
	public := s.publicUser(u, time.Now().UTC())
	s.store.RUnlock()
	jsonOut(w, 200, map[string]any{"user": public})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "leet_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookies()})
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.sessionUserID(r)
	if !ok {
		jsonError(w, 401, "Войди в аккаунт.")
		return
	}
	s.store.RLock()
	u := s.store.users[userID]
	if u == nil {
		s.store.RUnlock()
		jsonError(w, 401, "Аккаунт не найден.")
		return
	}
	data := s.publicUser(u, time.Now().UTC())
	s.store.RUnlock()
	jsonOut(w, 200, map[string]any{"user": data})
}
func (s *Server) progress(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.sessionUserID(r)
	if !ok {
		jsonError(w, 401, "Войди, чтобы сохранить прогресс.")
		return
	}
	var in struct {
		ExerciseID int "json:\"exerciseId\""
		FocusDelta int "json:\"focusDelta\""
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.ExerciseID < 1 || in.ExerciseID > 520 {
		jsonError(w, 400, "Задание не найдено.")
		return
	}
	s.store.Lock()
	defer s.store.Unlock()
	u := s.store.users[userID]
	if u == nil {
		jsonError(w, 401, "Аккаунт не найден.")
		return
	}
	if !s.hasAccess(u, time.Now().UTC()) {
		jsonError(w, 402, "Пробный день закончился. Продли доступ.")
		return
	}
	found := false
	for _, id := range u.Completed {
		if id == in.ExerciseID {
			found = true
			break
		}
	}
	if !found {
		u.Completed = append(u.Completed, in.ExerciseID)
	}
	if in.FocusDelta > 0 && in.FocusDelta < 3600 {
		u.FocusSeconds += in.FocusDelta
	}
	if err := s.store.saveLocked(); err != nil {
		jsonError(w, 500, "Не удалось сохранить прогресс.")
		return
	}
	jsonOut(w, 200, map[string]any{"user": s.publicUser(u, time.Now().UTC())})
}
func (s *Server) startPayment(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.sessionUserID(r)
	if !ok {
		jsonError(w, 401, "Войди, чтобы оформить доступ.")
		return
	}
	if s.telegramBotToken == "" || s.telegramWebhookSecret == "" {
		jsonError(w, http.StatusServiceUnavailable, "Оплата Telegram Stars не настроена.")
		return
	}
	s.store.RLock()
	user := s.store.users[userID]
	if user == nil || user.TelegramID == 0 {
		s.store.RUnlock()
		jsonError(w, http.StatusBadRequest, "Оплатить доступ можно из Telegram Mini App.")
		return
	}
	s.store.RUnlock()
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		jsonError(w, http.StatusInternalServerError, "Не удалось подготовить платёж.")
		return
	}
	label := "letri_" + hex.EncodeToString(raw)
	now := time.Now().UTC()
	payment := &Payment{Label: label, UserID: userID, Amount: monthlyPrice, Currency: "XTR", Status: "pending", CreatedAt: now}
	s.store.Lock()
	s.store.payments[label] = payment
	if err := s.store.saveLocked(); err != nil {
		delete(s.store.payments, label)
		s.store.Unlock()
		jsonError(w, http.StatusInternalServerError, "Не удалось подготовить платёж.")
		return
	}
	s.store.Unlock()
	invoiceLink, err := s.createTelegramInvoice(label)
	if err != nil {
		log.Printf("create Telegram Stars invoice: %v", err)
		s.store.Lock()
		payment.Status = "failed"
		if saveErr := s.store.saveLocked(); saveErr != nil {
			log.Printf("save failed Telegram Stars payment: %v", saveErr)
		}
		s.store.Unlock()
		jsonError(w, http.StatusBadGateway, "Не удалось создать счёт Telegram Stars. Попробуй ещё раз.")
		return
	}
	jsonOut(w, http.StatusOK, map[string]string{"invoiceLink": invoiceLink, "paymentId": label})
}

func (s *Server) paymentStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.sessionUserID(r)
	if !ok {
		jsonError(w, http.StatusUnauthorized, "Войди, чтобы проверить оплату.")
		return
	}
	paymentID := r.URL.Query().Get("id")
	s.store.RLock()
	payment := s.store.payments[paymentID]
	found := payment != nil
	owned := found && payment.UserID == userID
	paid := owned && payment.Status == "paid"
	s.store.RUnlock()
	if paymentID == "" || !owned {
		jsonError(w, http.StatusNotFound, "Платёж не найден.")
		return
	}
	jsonOut(w, http.StatusOK, map[string]bool{"paid": paid})
}

type telegramPreCheckoutQuery struct {
	ID             string       `json:"id"`
	From           telegramUser `json:"from"`
	Currency       string       `json:"currency"`
	TotalAmount    int          `json:"total_amount"`
	InvoicePayload string       `json:"invoice_payload"`
}

type telegramSuccessfulPayment struct {
	Currency                string `json:"currency"`
	TotalAmount             int    `json:"total_amount"`
	InvoicePayload          string `json:"invoice_payload"`
	TelegramPaymentChargeID string `json:"telegram_payment_charge_id"`
}

type telegramUpdate struct {
	PreCheckoutQuery *telegramPreCheckoutQuery `json:"pre_checkout_query"`
	Message          *struct {
		From              telegramUser               `json:"from"`
		SuccessfulPayment *telegramSuccessfulPayment `json:"successful_payment"`
	} `json:"message"`
}

func (s *Server) telegramWebhook(w http.ResponseWriter, r *http.Request) {
	if s.telegramBotToken == "" || s.telegramWebhookSecret == "" {
		http.Error(w, "webhook not configured", http.StatusServiceUnavailable)
		return
	}
	provided := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if !hmac.Equal([]byte(provided), []byte(s.telegramWebhookSecret)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var update telegramUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "invalid update", http.StatusBadRequest)
		return
	}
	if query := update.PreCheckoutQuery; query != nil {
		s.answerTelegramPreCheckout(w, query)
		return
	}
	if update.Message != nil && update.Message.SuccessfulPayment != nil {
		if err := s.applyTelegramPayment(update.Message.From.ID, update.Message.SuccessfulPayment); err != nil {
			log.Printf("apply Telegram Stars payment: %v", err)
			status := http.StatusInternalServerError
			if errors.Is(err, errInvalidTelegramPayment) {
				status = http.StatusBadRequest
			}
			http.Error(w, "payment not accepted", status)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) answerTelegramPreCheckout(w http.ResponseWriter, query *telegramPreCheckoutQuery) {
	s.store.RLock()
	payment := s.store.payments[query.InvoicePayload]
	valid := payment != nil && payment.Status == "pending" &&
		payment.Amount == monthlyPrice && payment.Currency == "XTR" &&
		payment.UserID == "telegram:"+strconv.FormatInt(query.From.ID, 10) &&
		query.Currency == "XTR" && query.TotalAmount == monthlyPrice
	s.store.RUnlock()
	answer := map[string]any{"pre_checkout_query_id": query.ID, "ok": valid}
	if !valid {
		answer["error_message"] = "Счёт не найден или его сумма изменилась. Создай новый счёт."
	}
	if err := s.callTelegram("answerPreCheckoutQuery", answer, nil); err != nil {
		log.Printf("answer Telegram pre-checkout query: %v", err)
		http.Error(w, "could not confirm pre-checkout", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) applyTelegramPayment(telegramID int64, paid *telegramSuccessfulPayment) error {
	if telegramID < 1 || paid.Currency != "XTR" || paid.TotalAmount != monthlyPrice ||
		paid.InvoicePayload == "" || paid.TelegramPaymentChargeID == "" {
		return errInvalidTelegramPayment
	}
	s.store.Lock()
	defer s.store.Unlock()
	if s.store.processed[paid.TelegramPaymentChargeID] {
		return nil
	}
	payment := s.store.payments[paid.InvoicePayload]
	userID := "telegram:" + strconv.FormatInt(telegramID, 10)
	if payment == nil || payment.Status != "pending" || payment.Amount != monthlyPrice ||
		payment.Currency != "XTR" || payment.UserID != userID {
		return errInvalidTelegramPayment
	}
	user := s.store.users[userID]
	if user == nil || user.TelegramID != telegramID {
		return errInvalidTelegramPayment
	}
	now := time.Now().UTC()
	start := now
	if user.TrialEndsAt.After(start) {
		start = user.TrialEndsAt
	}
	if user.PaidUntil.After(start) {
		start = user.PaidUntil
	}
	oldPaidUntil := user.PaidUntil
	user.PaidUntil = start.Add(monthLength)
	payment.Status = "paid"
	payment.ChargeID = paid.TelegramPaymentChargeID
	s.store.processed[paid.TelegramPaymentChargeID] = true
	if err := s.store.saveLocked(); err != nil {
		user.PaidUntil = oldPaidUntil
		payment.Status = "pending"
		payment.ChargeID = ""
		delete(s.store.processed, paid.TelegramPaymentChargeID)
		return fmt.Errorf("save paid subscription: %w", err)
	}
	return nil
}

var errInvalidTelegramPayment = errors.New("Telegram payment does not match a pending invoice")

func (s *Server) createTelegramInvoice(payload string) (string, error) {
	var result string
	request := map[string]any{
		"title":          "Летри — доступ на 30 дней",
		"description":    "Полный доступ к тренировкам и прогрессу на 30 дней.",
		"payload":        payload,
		"provider_token": "",
		"currency":       "XTR",
		"prices":         []map[string]any{{"label": "Доступ на 30 дней", "amount": monthlyPrice}},
	}
	if err := s.callTelegram("createInvoiceLink", request, &result); err != nil {
		return "", err
	}
	if result == "" {
		return "", errors.New("Telegram returned an empty invoice link")
	}
	return result, nil
}

func (s *Server) callTelegram(method string, request any, result any) error {
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode Telegram API request: %w", err)
	}
	endpoint := "https://api.telegram.org/bot" + s.telegramBotToken + "/" + method
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create Telegram API request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := s.telegramHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("call Telegram API: %w", err)
	}
	defer response.Body.Close()
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("decode Telegram API response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.OK {
		return fmt.Errorf("Telegram API %s failed: %s", method, envelope.Description)
	}
	if result != nil {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return fmt.Errorf("decode Telegram API result: %w", err)
		}
	}
	return nil
}
func (st *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(st.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(diskState{st.users, st.payments, st.processed}, "", "  ")
	if err != nil {
		return err
	}
	temp := st.path + ".tmp"
	if err = os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	return os.Rename(temp, st.path)
}
func loadStore(path string) (*Store, error) {
	st := &Store{users: map[string]*User{}, payments: map[string]*Payment{}, processed: map[string]bool{}, path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	var d diskState
	if err = json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	if d.Users != nil {
		st.users = d.Users
	}
	if d.Payments != nil {
		st.payments = d.Payments
	}
	if d.Processed != nil {
		st.processed = d.Processed
	}
	return st, nil
}
func generateExercises() []Exercise {
	images := []string{
		"на ладони дрожит рассвет", "в окне поселилась тишина", "ветер перелистывает облака", "фонарь хранит кусочек луны",
		"река считает мосты", "снег укрыл следы вчерашних мыслей", "чай остывает рядом с книгой", "поезд увозит эхо станции",
		"сад засыпает под шёпот дождя", "бумажный кораблик ищет море", "утро собирает рассыпанные звёзды", "город примеряет золотой шарф",
		"трамвай везёт домой тёплый свет", "на крыше разговаривают птицы", "часы берегут один спокойный миг", "тропинка ведёт к тихому дому",
		"капля рисует круги на воде", "в кармане лежит солнечный луч", "костёр рассказывает старую историю", "сосна качает зелёную колыбель",
		"запах хлеба возвращает детство", "письмо ждёт ответа у окна", "берег слушает медленные волны", "ночь рассыпала синие огни",
		"компас ищет дорогу сквозь туман", "колокольчик будит сонный двор",
	}
	templates := []TextTemplate{
		{"Стихи", "Строка о вечере", []string{"В тетради вечера осталась строка:", "«{{образ}}».", "Пусть день уходит не спеша,", "а мысль найдёт дорогу к свету."}},
		{"Стихи", "Маленький рассвет", []string{"Утро тихо открывает дверь,", "и на пороге — «{{образ}}».", "Сделай вдох и оглянись:", "мир уже начал новый день."}},
		{"Стихи", "Городская зарисовка", []string{"На перекрёстке сонных улиц", "вдруг появляется «{{образ}}».", "Шаг становится спокойней,", "а шум теряет остроту."}},
		{"Стихи", "Письмо себе", []string{"Оставь себе на завтра", "слова: «{{образ}}».", "Когда спешка станет громче,", "прочти их медленно ещё раз."}},
		{"Стихи", "Тихая вода", []string{"Вода не спорит с берегами,", "она запомнила «{{образ}}».", "Так и мысль становится ясней,", "когда ей дают немного места."}},
		{"Стихи", "Дорога домой", []string{"Дорога складывает шаги", "в короткую строку: «{{образ}}».", "За поворотом ждёт покой,", "за ним — тепло знакомых окон."}},
		{"Стихи", "Комната света", []string{"В самой простой из комнат", "нашлось место для «{{образ}}».", "И лампа светит чуть добрей,", "и вечер кажется короче."}},
		{"Стихи", "Заметка в полях", []string{"Между дел и важных дат", "помести «{{образ}}».", "Пусть память удержит главное,", "а остальное отпустит."}},
		{"Истории", "Случай на станции", []string{"На пустой платформе кто-то оставил записку:", "«{{образ}}».", "Дежурный улыбнулся и посмотрел на часы.", "До поезда оставалось ровно пять минут."}},
		{"Истории", "Ключ от сада", []string{"Под старой скамейкой блеснул маленький ключ.", "На бирке было написано: «{{образ}}».", "За калиткой пахло яблоками и мокрой землёй.", "В саду никто не торопился."}},
		{"Истории", "Книжная закладка", []string{"Между страниц выпала закладка с фразой", "«{{образ}}».", "Книга была раскрыта на главе о путешествии.", "За окном тихо шумел вечерний город."}},
		{"Истории", "Записка в трамвае", []string{"На сиденье лежал сложенный вчетверо лист.", "Внутри — только слова: «{{образ}}».", "Пассажир убрал записку в карман.", "На следующей остановке он вышел другим маршрутом."}},
		{"Истории", "Маяк", []string{"Смотритель каждый вечер зажигал маяк.", "В журнале он оставил фразу: «{{образ}}».", "Море отвечало ровным шумом волн.", "До рассвета оставалось немного."}},
		{"Истории", "Потерянный компас", []string{"Компас остановился у старой тропы.", "На крышке появилась надпись: «{{образ}}».", "Путник свернул с широкой дороги.", "Впереди начинался сосновый лес."}},
		{"Истории", "Окно на чердак", []string{"На чердаке было одно круглое окно.", "На стекле кто-то вывел: «{{образ}}».", "Внизу мирно скрипели ступени.", "Дом хранил истории нескольких поколений."}},
		{"Истории", "Письмо без адреса", []string{"Почтальон нашёл конверт без адреса.", "Внутри лежала строчка: «{{образ}}».", "Он оставил его на столе у входа.", "Вскоре за письмом пришла девочка с велосипедом."}},
		{"Практика внимания", "Один спокойный миг", []string{"Остановись на несколько секунд.", "Заметь образ: «{{образ}}».", "Рассмотри его без спешки.", "Вернись к дыханию и продолжай день."}},
		{"Практика внимания", "Точка опоры", []string{"Поставь стопы на пол и выпрями спину.", "Прочти фразу: «{{образ}}».", "Найди в ней одно ясное слово.", "Побудь с ним до следующего вдоха."}},
		{"Практика внимания", "Медленное чтение", []string{"Прочитай строки по одной.", "Среди них спрятано: «{{образ}}».", "После каждой строки сделай короткую паузу.", "Проверь, что внимание осталось здесь."}},
		{"Практика внимания", "Наблюдение", []string{"Не оценивай и не спеши.", "Просто отметь фразу: «{{образ}}».", "Пусть взгляд спокойно пройдёт по строкам.", "Заверши упражнение одним глубоким вдохом."}},
	}
	out := make([]Exercise, 0, len(images)*len(templates))
	for ti, t := range templates {
		for ii, image := range images {
			id := ti*len(images) + ii + 1
			level, name := levelFor(id)
			lines := make([]string, len(t.Lines))
			for n, line := range t.Lines {
				lines[n] = strings.ReplaceAll(line, "{{образ}}", image)
			}
			options, correct := answerOptions(images, ti, ii)
			out = append(out, Exercise{ID: id, Title: fmt.Sprintf("%s · %03d", t.Title, ii+1), Category: t.Category, Level: level, LevelName: name, Text: strings.Join(lines, "\n"), Question: "Какой образ спрятан в тексте?", Options: options, CorrectOption: correct})
		}
	}
	return out
}
func answerOptions(images []string, templateIndex, imageIndex int) ([]string, int) {
	n := len(images)
	indices := []int{imageIndex, (imageIndex + 5 + templateIndex%3) % n, (imageIndex + 11 + templateIndex%5) % n, (imageIndex + 19 + templateIndex%7) % n}
	correct := (templateIndex + imageIndex) % len(indices)
	indices[0], indices[correct] = indices[correct], indices[0]
	options := make([]string, 4)
	for i, index := range indices {
		options[i] = images[index]
	}
	return options, correct
}
func levelFor(id int) (int, string) {
	switch {
	case id <= 80:
		return 1, "Спокойно"
	case id <= 200:
		return 2, "Пульс"
	case id <= 360:
		return 3, "Вращение"
	default:
		return 4, "Микс"
	}
}
func (s *Server) publicUser(u *User, now time.Time) map[string]any {
	until := u.TrialEndsAt
	plan := "trial"
	if u.PaidUntil.After(until) {
		until = u.PaidUntil
		plan = "paid"
	}
	return map[string]any{"email": u.Email, "name": u.TelegramName, "telegram": u.TelegramID != 0, "createdAt": u.CreatedAt, "trialEndsAt": u.TrialEndsAt, "paidUntil": u.PaidUntil, "accessUntil": until, "accessActive": now.Before(until), "plan": plan, "completed": u.Completed, "focusSeconds": u.FocusSeconds, "emailVerified": u.EmailVerified, "totalExercises": 520}
}
func (s *Server) hasAccess(u *User, now time.Time) bool {
	until := u.TrialEndsAt
	if u.PaidUntil.After(until) {
		until = u.PaidUntil
	}
	return now.Before(until)
}
func (s *Server) setSession(w http.ResponseWriter, email string) {
	expires := time.Now().Add(14 * 24 * time.Hour).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(email)) + "." + strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, s.sessionSecret)
	_, _ = mac.Write([]byte(payload))
	token := payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{Name: "leet_session", Value: token, Path: "/", Expires: time.Unix(expires, 0), MaxAge: 14 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookies()})
}
func (s *Server) sessionUserID(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("leet_session")
	if err != nil {
		return "", false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 {
		return "", false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > expires {
		return "", false
	}
	mac := hmac.New(sha256.New, s.sessionSecret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	return string(raw), true
}
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const rounds = 80000
	hash := pbkdf2([]byte(password), salt, rounds, 32)
	return fmt.Sprintf("pbkdf2$%d$%s$%s", rounds, hex.EncodeToString(salt), hex.EncodeToString(hash)), nil
}
func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	rounds, err := strconv.Atoi(parts[1])
	if err != nil || rounds < 10000 || rounds > 1000000 {
		return false
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[3])
	if err != nil {
		return false
	}
	return hmac.Equal(pbkdf2([]byte(password), salt, rounds, len(want)), want)
}
func pbkdf2(password, salt []byte, rounds, keyLength int) []byte {
	const hashLength = 32
	result := make([]byte, 0, keyLength)
	for block := uint32(1); len(result) < keyLength; block++ {
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		_, _ = mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < rounds; i++ {
			mac = hmac.New(sha256.New, password)
			_, _ = mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		result = append(result, t...)
	}
	return result[:keyLength]
}
func randomDigits(count int) (string, error) {
	var out strings.Builder
	for i := 0; i < count; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		out.WriteByte(byte('0' + n.Int64()))
	}
	return out.String(), nil
}
func smtpReady() bool {
	return os.Getenv("SMTP_HOST") != "" && os.Getenv("SMTP_PORT") != "" && os.Getenv("SMTP_USERNAME") != "" && os.Getenv("SMTP_PASSWORD") != "" && os.Getenv("SMTP_FROM") != ""
}
func smtpPartial() bool {
	keys := []string{"SMTP_HOST", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM"}
	filled := 0
	for _, key := range keys {
		if os.Getenv(key) != "" {
			filled++
		}
	}
	return filled > 0 && !smtpReady()
}
func sendVerification(to, code string) error {
	host := os.Getenv("SMTP_HOST")
	address := host + ":" + os.Getenv("SMTP_PORT")
	auth := smtp.PlainAuth("", os.Getenv("SMTP_USERNAME"), os.Getenv("SMTP_PASSWORD"), host)
	body := "To: " + to + "\r\nSubject: Letri email verification\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nКод подтверждения: " + code + "\r\nДействует 30 минут.\r\n"
	return smtp.SendMail(address, auth, os.Getenv("SMTP_FROM"), []string{to}, []byte(body))
}
func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		jsonError(w, 400, "Проверь заполненные поля.")
		return false
	}
	return true
}
func jsonOut(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
func jsonError(w http.ResponseWriter, status int, message string) {
	jsonOut(w, status, map[string]string{"error": message})
}
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
func (s *Server) frontend() http.Handler {
	if _, err := os.Stat(filepath.Join("frontend", "dist")); err == nil {
		return http.FileServer(http.Dir(filepath.Join("frontend", "dist")))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("API работает. В frontend запусти npm install && npm run dev."))
			return
		}
		http.NotFound(w, r)
	})
}
func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == 34 && value[len(value)-1] == 34 {
			value = value[1 : len(value)-1]
		}
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("read .env: %v", err)
	}
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func secureCookies() bool { return strings.EqualFold(os.Getenv("APP_SECURE_COOKIES"), "true") }
