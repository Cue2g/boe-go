package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const resendEndpoint = "https://api.resend.com/emails"

// ResendConfig configura el envío por la API HTTP de Resend (https://resend.com).
type ResendConfig struct {
	APIKey string
	// From debe ser una dirección de un dominio verificado en Resend, p. ej.
	// "alertas@tudominio.com" o "Alertas SBOE <alertas@tudominio.com>".
	From string
	// Endpoint sustituye la URL de la API (solo para tests); vacío = API de Resend.
	Endpoint string
}

// ResendMailer envía emails con la API de Resend.
type ResendMailer struct {
	cfg      ResendConfig
	endpoint string
	client   *http.Client
}

func newResend(c ResendConfig) *ResendMailer {
	if !strings.Contains(c.From, "<") {
		c.From = "Alertas SBOE <" + c.From + ">"
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = resendEndpoint
	}
	return &ResendMailer{cfg: c, endpoint: endpoint, client: &http.Client{Timeout: 30 * time.Second}}
}

type resendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

type resendError struct {
	StatusCode int    `json:"statusCode"`
	Name       string `json:"name"`
	Message    string `json:"message"`
}

func (r *ResendMailer) Send(ctx context.Context, m Message) error {
	if strings.ContainsAny(m.To, "\r\n") || !strings.Contains(m.To, "@") {
		return fmt.Errorf("destinatario inválido: %q", m.To)
	}
	body, err := json.Marshal(resendRequest{From: r.cfg.From, To: []string{m.To}, Subject: m.Subject, HTML: m.HTML})
	if err != nil {
		return err
	}
	// Reintenta ante límite de peticiones (429) o errores del servidor (5xx).
	const attempts = 3
	for i := 1; ; i++ {
		wait, err := r.post(ctx, body)
		if err == nil || wait == 0 || i == attempts {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// post hace una petición; si falla con un error reintentable devuelve cuánto esperar.
func (r *ResendMailer) post(ctx context.Context, body []byte) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+r.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("resend: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 == 2 {
		return 0, nil
	}
	msg := strings.TrimSpace(string(data))
	var re resendError
	if json.Unmarshal(data, &re) == nil && re.Message != "" {
		msg = re.Name + ": " + re.Message
	}
	err = fmt.Errorf("resend: HTTP %d: %s", resp.StatusCode, msg)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		wait := 2 * time.Second
		if s, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && s > 0 && s <= 60 {
			wait = time.Duration(s) * time.Second
		}
		return wait, err
	}
	return 0, err
}
