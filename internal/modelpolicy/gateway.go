package modelpolicy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Gateway pins one agent attempt to one deployment, with router fallbacks and
// retries disabled. It observes actual API errors, including SSE failures,
// before stopping the attempt. A replacement model gets a fresh agent context.
type Gateway struct {
	URL string
	Key string

	server            *http.Server
	transport         *http.Transport
	mu                sync.Mutex
	failure           *Failure
	stop              func()
	accountant        Accountant
	accountingContext context.Context
	accountingError   error
	gate              sync.Mutex
	closed            bool
	active            sync.WaitGroup
	closeOnce         sync.Once
}

// Accountant persists admission and receipts. Begin serializes unsettled calls
// when a spending cap is enabled; it never guesses a missing cost is zero.
type Accountant interface {
	Begin(context.Context) (string, error)
	Record(id string, cost float64, input, output int) error
	Finish(context.Context, string) error
}

type GatewayOptions struct {
	Accountant Accountant
	// AccountingContext outlives an individual agent attempt or client stream,
	// allowing delayed router callbacks to settle a failed request.
	AccountingContext context.Context
}

type AccountingError struct{ Err error }

func (e *AccountingError) Error() string { return "project budget: " + e.Err.Error() }
func (e *AccountingError) Unwrap() error { return e.Err }

type receiptKey struct{}
type requestReceipt struct {
	id        string
	accounted bool
	gateway   *Gateway
}

func StartGateway(baseURL, key, route string, stop func(), options ...GatewayOptions) (*Gateway, error) {
	target, err := url.Parse(baseURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, errors.New("invalid model router URL")
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	g := &Gateway{URL: "http://" + listener.Addr().String() + "/v1", Key: hex.EncodeToString(secret[:]), stop: stop}
	g.accountingContext = context.Background()
	if len(options) > 0 {
		g.accountant = options[0].Accountant
		if options[0].AccountingContext != nil {
			g.accountingContext = options[0].AccountingContext
		}
	}
	g.transport = http.DefaultTransport.(*http.Transport).Clone()
	proxy := &httputil.ReverseProxy{
		Transport: g.transport,
		// Flush each SSE fragment; buffering would delay the coding agent.
		FlushInterval: -1,
		ErrorLog:      log.New(io.Discard, "", 0),
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			p.Out.URL.Path = strings.TrimRight(target.Path, "/") + strings.TrimPrefix(p.In.URL.Path, "/v1")
			p.Out.URL.RawPath, p.Out.URL.RawQuery = "", ""
			p.Out.Host = target.Host
			p.Out.Header.Set("Authorization", "Bearer "+key)
			p.Out.Header.Set("X-Litellm-Num-Retries", "0")
			p.Out.Header.Del("Accept-Encoding")
		},
		ModifyResponse: func(resp *http.Response) error {
			receipt, _ := resp.Request.Context().Value(receiptKey{}).(*requestReceipt)
			if resp.StatusCode >= 400 {
				body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
				resp.Body.Close()
				if err != nil {
					return err
				}
				resp.Body = io.NopCloser(bytes.NewReader(body))
				resp.ContentLength = int64(len(body))
				resp.Header.Del("Content-Length")
				if receipt != nil {
					if cost, err := strconv.ParseFloat(resp.Header.Get("X-Litellm-Response-Cost"), 64); err == nil {
						receipt.record(cost, 0, 0)
					}
					// A rejected request never began generation. A streaming or
					// transport failure remains pending until a real receipt arrives.
					if !receipt.accounted && (resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 402 || resp.StatusCode == 403 || resp.StatusCode == 404 || resp.StatusCode == 413 || resp.StatusCode == 422 || resp.StatusCode == 429) {
						receipt.record(0, 0, 0)
					}
				}
				g.record(Classify(resp.StatusCode, resp.Header, body, time.Now()))
			} else if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
				// Streaming headers often contain a premature zero cost. Only
				// terminal SSE receipts or authenticated callbacks settle a stream.
				resp.Body = &eventReader{ReadCloser: resp.Body, observe: func(event []byte) {
					if receipt != nil {
						receipt.event(event)
					}
					g.observeEvent(event, resp.Header)
				}}
			} else if receipt != nil {
				if cost, err := strconv.ParseFloat(resp.Header.Get("X-Litellm-Response-Cost"), 64); err == nil {
					receipt.record(cost, 0, 0)
				}
				resp.Body = &responseReader{ReadCloser: resp.Body, done: receipt.response}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			// Transport failures have no provider evidence. Do not drain every
			// provider when the local router itself is down.
			http.Error(w, "model router connection failed", http.StatusBadGateway)
		},
	}
	g.server = &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		g.gate.Lock()
		if g.closed {
			g.gate.Unlock()
			http.Error(w, "model attempt has ended", http.StatusServiceUnavailable)
			return
		}
		g.active.Add(1)
		g.gate.Unlock()
		defer g.active.Done()
		if req.Header.Get("Authorization") != "Bearer "+g.Key {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if req.Method != http.MethodPost || (req.URL.Path != "/v1/responses" && req.URL.Path != "/v1/responses/compact") {
			http.Error(w, "unsupported agent endpoint", http.StatusNotFound)
			return
		}
		if g.Failure() != nil || g.AccountingError() != nil {
			http.Error(w, "model attempt has ended", http.StatusServiceUnavailable)
			return
		}
		var body map[string]json.RawMessage
		data, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 32<<20))
		if err != nil || json.Unmarshal(data, &body) != nil || body == nil {
			http.Error(w, "invalid model request", http.StatusBadRequest)
			return
		}
		body["model"], _ = json.Marshal(route)
		body["disable_fallbacks"] = json.RawMessage("true")
		body["num_retries"] = json.RawMessage("0")
		for _, field := range []string{"fallbacks", "context_window_fallbacks", "content_policy_fallbacks"} {
			delete(body, field)
		}
		if g.accountant != nil {
			id, err := g.accountant.Begin(req.Context())
			if err != nil {
				g.abort(err)
				http.Error(w, "project budget prevents this request", http.StatusPaymentRequired)
				return
			}
			receipt := &requestReceipt{id: id, gateway: g}
			req = req.WithContext(context.WithValue(req.Context(), receiptKey{}, receipt))
			metadata, _ := json.Marshal(map[string]string{"dotshift_request_id": id})
			req.Header.Set("X-Litellm-Spend-Logs-Metadata", string(metadata))
			// For Responses, LiteLLM keeps its metadata separate from the
			// provider's string-only metadata object.
			body["litellm_metadata"], _ = json.Marshal(map[string]any{"spend_logs_metadata": map[string]string{"dotshift_request_id": id}})
			defer func() {
				if !receipt.accounted {
					if err := g.accountant.Finish(g.accountingContext, id); err != nil {
						g.abort(err)
					}
				}
			}()
		}
		data, _ = json.Marshal(body)
		req.Body = io.NopCloser(bytes.NewReader(data))
		req.ContentLength = int64(len(data))
		req.Header.Set("Content-Type", "application/json")
		proxy.ServeHTTP(w, req)
	})}
	go g.server.Serve(listener)
	return g, nil
}

func (g *Gateway) Close() {
	g.closeOnce.Do(func() {
		g.gate.Lock()
		g.closed = true
		g.gate.Unlock()
		_ = g.server.Close()
		g.active.Wait()
		g.transport.CloseIdleConnections()
	})
}

func (g *Gateway) AccountingError() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.accountingError
}

func (g *Gateway) abort(err error) {
	if err == nil {
		return
	}
	g.mu.Lock()
	first := g.accountingError == nil
	if first {
		g.accountingError = &AccountingError{Err: err}
	}
	g.mu.Unlock()
	if first && g.stop != nil {
		g.stop()
	}
}

func (r *requestReceipt) record(cost float64, input, output int) {
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
		return
	}
	if err := r.gateway.accountant.Record(r.id, cost, input, output); err != nil {
		r.gateway.abort(err)
		return
	}
	r.accounted = true
}

func (r *requestReceipt) response(data []byte) {
	var body struct {
		ResponseCost *float64 `json:"response_cost"`
		Usage        struct {
			Cost         *float64 `json:"cost"`
			ResponseCost *float64 `json:"response_cost"`
			Input        int      `json:"input_tokens"`
			Output       int      `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &body) != nil {
		return
	}
	cost := body.Usage.Cost
	if cost == nil {
		cost = body.Usage.ResponseCost
	}
	if cost == nil {
		cost = body.ResponseCost
	}
	if cost != nil {
		r.record(*cost, body.Usage.Input, body.Usage.Output)
	}
}

func (r *requestReceipt) event(event []byte) {
	var body struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(eventData(event), &body) != nil {
		return
	}
	if body.Type == "response.completed" || body.Type == "response.incomplete" || body.Type == "response.failed" {
		r.response(body.Response)
	}
}

type responseReader struct {
	io.ReadCloser
	buffer   []byte
	done     func([]byte)
	finished bool
}

func (r *responseReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if len(r.buffer)+n <= 1<<20 {
		r.buffer = append(r.buffer, p[:n]...)
	} else {
		r.finished = true
		r.buffer = nil
	}
	if err == io.EOF && !r.finished {
		r.finished = true
		r.done(r.buffer)
	}
	return n, err
}

func (g *Gateway) Failure() *Failure {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failure == nil {
		return nil
	}
	failure := *g.failure
	return &failure
}

func (g *Gateway) record(failure *Failure) {
	if failure == nil {
		return
	}
	g.mu.Lock()
	first := g.failure == nil
	if first {
		g.failure = failure
	}
	g.mu.Unlock()
	if first && g.stop != nil {
		g.stop()
	}
}

func (g *Gateway) observeEvent(event []byte, headers http.Header) {
	data := eventData(event)
	var envelope struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return
	}
	switch envelope.Type {
	case "error":
		g.record(Classify(200, headers, data, time.Now()))
	case "response.failed":
		g.record(Classify(200, headers, envelope.Response, time.Now()))
	}
}

func eventData(event []byte) []byte {
	var data []byte
	for _, line := range bytes.Split(event, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("data:")) {
			data = append(data, bytes.TrimSpace(line[5:])...)
			data = append(data, '\n')
		}
	}
	return data
}

// eventReader observes SSE without delaying fragments or retaining a full
// response. Oversized events are skipped until the next event boundary.
type eventReader struct {
	io.ReadCloser
	observe func([]byte)
	buffer  []byte
	dropped bool
	newline bool
}

func (r *eventReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	for _, b := range p[:n] {
		if b == '\r' {
			continue
		}
		if b == '\n' && r.newline {
			if !r.dropped {
				r.observe(r.buffer)
			}
			r.buffer, r.dropped, r.newline = r.buffer[:0], false, false
			continue
		}
		r.newline = b == '\n'
		if !r.dropped {
			if len(r.buffer) >= 1<<20 {
				r.buffer, r.dropped = r.buffer[:0], true
			} else {
				r.buffer = append(r.buffer, b)
			}
		}
	}
	return n, err
}
