package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestValidateTelegramInitData(t *testing.T) {
	const token = "test-bot-token"
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	user := telegramUser{ID: 42, FirstName: "Ada", Username: "ada"}
	initData := signedInitData(t, token, user, now.Unix())

	got, err := validateTelegramInitData(initData, token, now)
	if err != nil {
		t.Fatalf("validateTelegramInitData() error = %v", err)
	}
	if got != user {
		t.Fatalf("validateTelegramInitData() = %#v, want %#v", got, user)
	}

	if _, err := validateTelegramInitData(initData, "wrong-token", now); err == nil {
		t.Fatal("validateTelegramInitData() accepted data signed with another bot token")
	}
	if _, err := validateTelegramInitData(signedInitData(t, token, user, now.Add(-25*time.Hour).Unix()), token, now); err == nil {
		t.Fatal("validateTelegramInitData() accepted expired init data")
	}
	if _, err := validateTelegramInitData(initData+"&auth_date="+strconv.FormatInt(now.Unix(), 10), token, now); err == nil {
		t.Fatal("validateTelegramInitData() accepted a duplicate parameter")
	}
}

func TestApplyTelegramPaymentExtendsAccessOnce(t *testing.T) {
	const telegramID = int64(42)
	const userID = "telegram:42"
	store, err := loadStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.users[userID] = &User{
		TelegramID:  telegramID,
		TrialEndsAt: time.Now().UTC().Add(-48 * time.Hour),
		PaidUntil:   time.Now().UTC().Add(24 * time.Hour),
		Completed:   []int{},
	}
	store.payments["invoice-id"] = &Payment{
		Label: "invoice-id", UserID: userID, Amount: monthlyPrice, Currency: "XTR", Status: "pending",
	}
	server := &Server{store: store}
	payment := &telegramSuccessfulPayment{
		Currency: "XTR", TotalAmount: 200, InvoicePayload: "invoice-id", TelegramPaymentChargeID: "charge-id",
	}
	previousUntil := store.users[userID].PaidUntil

	if err := server.applyTelegramPayment(telegramID, payment); err != nil {
		t.Fatalf("applyTelegramPayment() error = %v", err)
	}
	paidUntil := store.users[userID].PaidUntil
	if !paidUntil.Equal(previousUntil.Add(monthLength)) {
		t.Fatalf("PaidUntil = %v, want %v", paidUntil, previousUntil.Add(monthLength))
	}
	if store.payments["invoice-id"].Status != "paid" {
		t.Fatal("payment status was not set to paid")
	}
	if err := server.applyTelegramPayment(telegramID, payment); err != nil {
		t.Fatalf("replayed applyTelegramPayment() error = %v", err)
	}
	if !store.users[userID].PaidUntil.Equal(paidUntil) {
		t.Fatal("replayed charge extended access more than once")
	}
}

func TestCreateTelegramInvoiceUsesStarsPrice(t *testing.T) {
	server := &Server{
		telegramBotToken: "test-bot-token",
		telegramHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/bottest-bot-token/createInvoiceLink" {
				t.Fatalf("Telegram API path = %q", request.URL.Path)
			}
			var body struct {
				Payload       string `json:"payload"`
				ProviderToken string `json:"provider_token"`
				Currency      string `json:"currency"`
				Prices        []struct {
					Amount int `json:"amount"`
				} `json:"prices"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Payload != "invoice-id" || body.ProviderToken != "" || body.Currency != "XTR" ||
				len(body.Prices) != 1 || body.Prices[0].Amount != 200 {
				t.Fatalf("invoice request = %#v", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":"https://t.me/$invoice"}`)),
			}, nil
		})},
	}
	link, err := server.createTelegramInvoice("invoice-id")
	if err != nil {
		t.Fatalf("createTelegramInvoice() error = %v", err)
	}
	if link != "https://t.me/$invoice" {
		t.Fatalf("createTelegramInvoice() = %q", link)
	}
}

func signedInitData(t *testing.T, token string, user telegramUser, authDate int64) string {
	t.Helper()
	userJSON, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{
		"auth_date": {strconv.FormatInt(authDate, 10)},
		"query_id":  {"query-1"},
		"user":      {string(userJSON)},
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+values.Get(key))
	}
	secretMAC := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretMAC.Write([]byte(token))
	checkMAC := hmac.New(sha256.New, secretMAC.Sum(nil))
	_, _ = checkMAC.Write([]byte(strings.Join(lines, "\n")))
	values.Set("hash", hex.EncodeToString(checkMAC.Sum(nil)))
	return values.Encode()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
