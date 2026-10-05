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
const monthlyPrice = 150

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
}
type Payment struct {
	Label       string    "json:\"label\""
	Email       string    "json:\"email\""
	Amount      int       "json:\"amount\""
	Status      string    "json:\"status\""
	CreatedAt   time.Time "json:\"createdAt\""
	OperationID string    "json:\"operationId,omitempty\""
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
	store         *Store
	sessionSecret []byte
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
	s := &Server{store: store, sessionSecret: []byte(secret)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/exercises", s.exercises)
	mux.HandleFunc("POST /api/register", s.register)
	mux.HandleFunc("POST /api/verify-email", s.verifyEmail)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("POST /api/progress", s.progress)
	mux.HandleFunc("POST /api/payment/start", s.startPayment)
	mux.HandleFunc("POST /api/yoomoney/notify", s.yoomoneyNotify)
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
	email, ok := s.sessionEmail(r)
	if !ok {
		jsonError(w, 401, "Войди в аккаунт.")
		return
	}
	s.store.RLock()
	u := s.store.users[email]
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
	email, ok := s.sessionEmail(r)
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
	u := s.store.users[email]
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
	email, ok := s.sessionEmail(r)
	if !ok {
		jsonError(w, 401, "Войди, чтобы оформить доступ.")
		return
	}
	receiver := strings.TrimSpace(os.Getenv("YOOMONEY_RECEIVER"))
	if receiver == "" || os.Getenv("YOOMONEY_NOTIFICATION_SECRET") == "" {
		jsonError(w, 503, "Оплата не настроена. Добавь реквизиты ЮMoney в .env.")
		return
	}
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		jsonError(w, 500, "Не удалось подготовить платёж.")
		return
	}
	label := "leet_" + hex.EncodeToString(raw)
	now := time.Now().UTC()
	payment := &Payment{Label: label, Email: email, Amount: monthlyPrice, Status: "pending", CreatedAt: now}
	s.store.Lock()
	s.store.payments[label] = payment
	if err := s.store.saveLocked(); err != nil {
		delete(s.store.payments, label)
		s.store.Unlock()
		jsonError(w, 500, "Не удалось подготовить платёж.")
		return
	}
	s.store.Unlock()
	base := env("APP_BASE_URL", "http://localhost:8080")
	jsonOut(w, 200, map[string]string{"action": "https://yoomoney.ru/quickpay/confirm", "receiver": receiver, "quickpay-form": "button", "paymentType": "AC", "sum": strconv.Itoa(monthlyPrice), "label": label, "successURL": base + "/?payment=waiting"})
}
func (s *Server) yoomoneyNotify(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	sign := r.Form.Get("sign")
	secret := os.Getenv("YOOMONEY_NOTIFICATION_SECRET")
	if sign == "" || secret == "" || !hmac.Equal([]byte(sign), []byte(yoomoneySign(r.Form, secret))) {
		http.Error(w, "invalid signature", 403)
		return
	}
	if r.Form.Get("currency") != "643" || r.Form.Get("codepro") == "true" || r.Form.Get("unaccepted") == "true" {
		http.Error(w, "payment not accepted", 400)
		return
	}
	amount, err := parseCents(r.Form.Get("withdraw_amount"))
	if err != nil || amount != monthlyPrice*100 {
		http.Error(w, "incorrect amount", 400)
		return
	}
	kind := r.Form.Get("notification_type")
	if kind != "p2p-incoming" && kind != "card-incoming" {
		http.Error(w, "unsupported transfer", 400)
		return
	}
	label := r.Form.Get("label")
	operation := r.Form.Get("operation_id")
	if label == "" || operation == "" {
		http.Error(w, "missing reference", 400)
		return
	}
	s.store.Lock()
	defer s.store.Unlock()
	if s.store.processed[operation] {
		w.WriteHeader(200)
		return
	}
	payment := s.store.payments[label]
	if payment == nil || payment.Status != "pending" || payment.Amount != monthlyPrice {
		http.Error(w, "unknown payment", 404)
		return
	}
	u := s.store.users[payment.Email]
	if u == nil {
		http.Error(w, "account not found", 404)
		return
	}
	start := time.Now().UTC()
	if u.TrialEndsAt.After(start) {
		start = u.TrialEndsAt
	}
	if u.PaidUntil.After(start) {
		start = u.PaidUntil
	}
	u.PaidUntil = start.Add(monthLength)
	payment.Status = "paid"
	payment.OperationID = operation
	s.store.processed[operation] = true
	if err := s.store.saveLocked(); err != nil {
		http.Error(w, "storage error", 500)
		return
	}
	w.WriteHeader(200)
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
	return map[string]any{"email": u.Email, "createdAt": u.CreatedAt, "trialEndsAt": u.TrialEndsAt, "paidUntil": u.PaidUntil, "accessUntil": until, "accessActive": now.Before(until), "plan": plan, "completed": u.Completed, "focusSeconds": u.FocusSeconds, "emailVerified": u.EmailVerified, "totalExercises": 520}
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
func (s *Server) sessionEmail(r *http.Request) (string, bool) {
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
func yoomoneySign(form url.Values, secret string) string {
	keys := make([]string, 0, len(form))
	for key := range form {
		if key != "sign" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+rfc3986(form.Get(key)))
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strings.Join(parts, "&")))
	return hex.EncodeToString(mac.Sum(nil))
}
func rfc3986(value string) string {
	const digits = "0123456789ABCDEF"
	var out strings.Builder
	for _, b := range []byte(value) {
		if b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.' || b == '~' {
			out.WriteByte(b)
		} else {
			out.WriteByte('%')
			out.WriteByte(digits[b>>4])
			out.WriteByte(digits[b&15])
		}
	}
	return out.String()
}
func parseCents(value string) (int, error) {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("invalid amount")
	}
	rubles, err := strconv.Atoi(parts[0])
	if err != nil || rubles < 0 {
		return 0, fmt.Errorf("invalid amount")
	}
	kopecks := 0
	if len(parts) == 2 {
		digits := parts[1]
		if len(digits) > 2 {
			return 0, fmt.Errorf("invalid amount")
		}
		if len(digits) == 1 {
			digits += "0"
		}
		if digits != "" {
			kopecks, err = strconv.Atoi(digits)
			if err != nil {
				return 0, fmt.Errorf("invalid amount")
			}
		}
	}
	return rubles*100 + kopecks, nil
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
