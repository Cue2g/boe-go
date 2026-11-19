package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResendSend(t *testing.T) {
	var got resendRequest
	var auth string
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"statusCode":429,"name":"rate_limit_exceeded","message":"Too many requests"}`))
			return
		}
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"id":"abc"}`))
	}))
	defer srv.Close()

	m := newResend(ResendConfig{APIKey: "re_test", From: "alertas@example.com", Endpoint: srv.URL})
	err := m.Send(context.Background(), Message{To: "a@b.com", Subject: "Hola ñ", HTML: "<p>x</p>"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (un reintento tras 429)", calls)
	}
	if auth != "Bearer re_test" {
		t.Errorf("auth = %q", auth)
	}
	if got.From != "Alertas SBOE <alertas@example.com>" || len(got.To) != 1 || got.To[0] != "a@b.com" || got.Subject != "Hola ñ" || got.HTML != "<p>x</p>" {
		t.Errorf("payload = %+v", got)
	}
}

func TestResendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"statusCode":403,"name":"validation_error","message":"The domain is not verified"}`))
	}))
	defer srv.Close()
	m := newResend(ResendConfig{APIKey: "k", From: "X <x@example.com>", Endpoint: srv.URL})
	err := m.Send(context.Background(), Message{To: "a@b.com", Subject: "s", HTML: "h"})
	if err == nil || !strings.Contains(err.Error(), "domain is not verified") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewPicksResend(t *testing.T) {
	smtp := SMTPConfig{Host: "smtp.example.com", User: "u@example.com", Pass: "p"}
	if _, ok := New(Config{Resend: ResendConfig{APIKey: "re_x", From: "a@example.com"}, SMTP: smtp}).(*ResendMailer); !ok {
		t.Error("con API key debe usar Resend aunque haya SMTP")
	}
	if _, ok := New(Config{SMTP: smtp}).(*SMTPMailer); !ok {
		t.Error("sin API key y con SMTP debe usar SMTP")
	}
	if _, ok := New(Config{}).(LogMailer); !ok {
		t.Error("sin nada configurado debe usar LogMailer")
	}
	m := New(Config{Resend: ResendConfig{APIKey: "re_x"}}).(*ResendMailer)
	if m.cfg.From != "Alertas SBOE <onboarding@resend.dev>" {
		t.Errorf("from por defecto = %q", m.cfg.From)
	}
}
