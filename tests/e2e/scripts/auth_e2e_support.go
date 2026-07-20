package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxBodyBytes           = 16 << 10
	sessionStatusKeyPrefix = "auth:session-status:v2:"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func main() {
	if len(os.Args) != 2 {
		fatal("usage: auth-e2e-support provider|echo|control")
	}
	adminToken := strings.TrimSpace(os.Getenv("AUTH_E2E_ADMIN_TOKEN"))
	if adminToken == "" {
		fatal("AUTH_E2E_ADMIN_TOKEN is required")
	}
	var handler http.Handler
	switch os.Args[1] {
	case "provider":
		provider, err := newProvider(adminToken)
		if err != nil {
			fatal(err.Error())
		}
		handler = provider.routes()
	case "echo":
		handler = newEcho(adminToken).routes()
	case "control":
		control, err := newControl(adminToken)
		if err != nil {
			fatal(err.Error())
		}
		handler = control.routes()
	case "edge-probe":
		handler = edgeProbeRoutes(adminToken)
	default:
		fatal("unsupported support mode")
	}
	server := &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal("support server stopped")
	}
}

func edgeProbeRoutes(adminToken string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /admin/assert/protected-bypass", adminOnly(adminToken, func(w http.ResponseWriter, r *http.Request) {
		request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://protected-echo:8080/protected", nil)
		client := &http.Client{Timeout: 500 * time.Millisecond}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			writeJSON(w, http.StatusConflict, map[string]bool{"blocked": false})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"blocked": true})
	}))
	return mux
}

func fatal(message string) {
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
		return false
	}
	return true
}

func authorized(r *http.Request, expected string) bool {
	provided := strings.TrimSpace(r.Header.Get("Authorization"))
	want := "Bearer " + expected
	return len(provided) == len(want) && subtle.ConstantTimeCompare([]byte(provided), []byte(want)) == 1
}

func adminOnly(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		next(w, r)
	}
}

type providerFault struct {
	Status    int
	Delay     time.Duration
	Remaining int
}

type providerMessage struct {
	Code      string
	RequestID string
}

type providerState struct {
	mu       sync.Mutex
	messages map[string]providerMessage
	accepted map[string]string
	attempts map[string]int
	faults   map[string]providerFault
}

type providerServer struct {
	adminToken    string
	providerToken string
	proofKeyID    string
	proofKey      ed25519.PrivateKey
	state         providerState
}

func newProvider(adminToken string) (*providerServer, error) {
	providerToken := strings.TrimSpace(os.Getenv("AUTH_E2E_PROVIDER_TOKEN"))
	encodedKey := strings.TrimSpace(os.Getenv("AUTH_E2E_USER_PROOF_PRIVATE_KEY"))
	proofKeyID := strings.TrimSpace(os.Getenv("AUTH_E2E_USER_PROOF_KEY_ID"))
	key, err := base64.RawStdEncoding.DecodeString(encodedKey)
	if providerToken == "" || err != nil || len(key) != ed25519.PrivateKeySize || proofKeyID == "" {
		return nil, errors.New("provider token and User proof signing key are required")
	}
	return &providerServer{
		adminToken: adminToken, providerToken: providerToken, proofKeyID: proofKeyID,
		proofKey: ed25519.PrivateKey(key),
		state: providerState{
			messages: make(map[string]providerMessage), accepted: make(map[string]string),
			attempts: make(map[string]int), faults: make(map[string]providerFault),
		},
	}, nil
}

func (s *providerServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/email", s.deliver("email"))
	mux.HandleFunc("POST /v1/sms", s.deliver("sms"))
	mux.HandleFunc("POST /admin/provider/fault", adminOnly(s.adminToken, s.setFault))
	mux.HandleFunc("POST /admin/provider/reset", adminOnly(s.adminToken, s.reset))
	mux.HandleFunc("POST /admin/provider/latest", adminOnly(s.adminToken, s.latest))
	mux.HandleFunc("GET /admin/provider/stats", adminOnly(s.adminToken, s.stats))
	mux.HandleFunc("POST /admin/proofs/user-creation", adminOnly(s.adminToken, s.userCreationProof))
	mux.HandleFunc("POST /admin/proofs/user-status", adminOnly(s.adminToken, s.userStatusProof))
	return mux
}

func (s *providerServer) deliver(channel string) http.HandlerFunc {
	type requestBody struct {
		DeliveryID  string `json:"deliveryId"`
		Destination string `json:"destination"`
		Code        string `json:"code"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, s.providerToken) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "provider_unauthorized"})
			return
		}
		var request requestBody
		if !decodeJSON(w, r, &request) {
			return
		}
		if !uuidPattern.MatchString(request.DeliveryID) || strings.TrimSpace(request.Destination) == "" || len(request.Code) != 6 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "provider_invalid_request"})
			return
		}
		key := channel + "\x00" + request.Destination
		s.state.mu.Lock()
		s.state.attempts[channel]++
		if requestID, ok := s.state.accepted[request.DeliveryID]; ok {
			s.state.mu.Unlock()
			writeJSON(w, http.StatusAccepted, map[string]string{"requestId": requestID})
			return
		}
		message := providerMessage{Code: request.Code}
		s.state.messages[key] = message
		fault := s.state.faults[channel]
		if fault.Remaining > 0 {
			fault.Remaining--
			s.state.faults[channel] = fault
		} else {
			fault = providerFault{}
		}
		s.state.mu.Unlock()

		if fault.Delay > 0 {
			timer := time.NewTimer(fault.Delay)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
				return
			case <-timer.C:
			}
		}
		if fault.Status != 0 && fault.Status != http.StatusAccepted {
			writeJSON(w, fault.Status, map[string]string{"code": "provider_fault"})
			return
		}
		digest := sha256.Sum256([]byte(request.DeliveryID))
		requestID := "req_" + base64.RawURLEncoding.EncodeToString(digest[:12])
		s.state.mu.Lock()
		message = s.state.messages[key]
		message.RequestID = requestID
		s.state.messages[key] = message
		s.state.accepted[request.DeliveryID] = requestID
		s.state.mu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]string{"requestId": requestID})
	}
}

func (s *providerServer) setFault(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Channel     string `json:"channel"`
		Status      int    `json:"status"`
		Failures    int    `json:"failures"`
		DelayMillis int    `json:"delayMillis"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if (request.Channel != "email" && request.Channel != "sms") || (request.Status != 0 && request.Status != 429 && (request.Status < 500 || request.Status > 599)) || request.Failures < 0 || request.Failures > 10 || request.DelayMillis < 0 || request.DelayMillis > 5000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_fault"})
		return
	}
	s.state.mu.Lock()
	s.state.faults[request.Channel] = providerFault{Status: request.Status, Delay: time.Duration(request.DelayMillis) * time.Millisecond, Remaining: request.Failures}
	s.state.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"configured": true})
}

func (s *providerServer) reset(w http.ResponseWriter, _ *http.Request) {
	s.state.mu.Lock()
	s.state.messages = make(map[string]providerMessage)
	s.state.accepted = make(map[string]string)
	s.state.attempts = make(map[string]int)
	s.state.faults = make(map[string]providerFault)
	s.state.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"reset": true})
}

func (s *providerServer) latest(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Channel     string `json:"channel"`
		Destination string `json:"destination"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	channel, destination := request.Channel, request.Destination
	if (channel != "email" && channel != "sms") || destination == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_lookup"})
		return
	}
	s.state.mu.Lock()
	message, ok := s.state.messages[channel+"\x00"+destination]
	if ok && message.RequestID != "" {
		delete(s.state.messages, channel+"\x00"+destination)
	}
	s.state.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "message_not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": message.Code, "accepted": message.RequestID != ""})
}

func (s *providerServer) stats(w http.ResponseWriter, _ *http.Request) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"attempts": s.state.attempts, "acceptedUnique": len(s.state.accepted)})
}

func (s *providerServer) userCreationProof(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RegistrationID string `json:"registrationId"`
		UserID         string `json:"userId"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if !uuidPattern.MatchString(request.RegistrationID) || !uuidPattern.MatchString(request.UserID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_proof_request"})
		return
	}
	proof, err := s.signProof(map[string]any{
		"aud": "auth-service", "purpose": "complete_registration", "registrationId": request.RegistrationID,
		"userId": request.UserID, "userVersion": 1, "emailVerified": true, "phoneVerified": true,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "proof_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"proof": proof})
}

func (s *providerServer) userStatusProof(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UserID        string `json:"userId"`
		AccountStatus string `json:"accountStatus"`
		UserVersion   int64  `json:"userVersion"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if !uuidPattern.MatchString(request.UserID) || request.UserVersion < 1 || (request.AccountStatus != "active" && request.AccountStatus != "restricted" && request.AccountStatus != "deactivated") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_proof_request"})
		return
	}
	now := time.Now().UTC()
	proof, err := s.signProof(map[string]any{
		"aud": "auth-service", "purpose": "apply_user_status", "statusChangeId": "e2e-" + strconv.FormatInt(now.UnixNano(), 36),
		"userId": request.UserID, "accountStatus": request.AccountStatus, "userVersion": request.UserVersion, "changedAt": now.Unix(),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "proof_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"proof": proof})
}

func (s *providerServer) signProof(claims map[string]any) (string, error) {
	now := time.Now().UTC()
	claims["iss"] = "user-service"
	claims["iat"] = now.Unix()
	claims["exp"] = now.Add(2 * time.Minute).Unix()
	claims["nonce"] = "e2e-" + strconv.FormatInt(now.UnixNano(), 36)
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": s.proofKeyID})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(s.proofKey, []byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

type echoServer struct {
	adminToken string
	mu         sync.Mutex
	count      int
	last       map[string]string
}

func newEcho(adminToken string) *echoServer {
	return &echoServer{adminToken: adminToken, last: make(map[string]string)}
}

func (s *echoServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /protected", s.protected)
	mux.HandleFunc("GET /admin/state", adminOnly(s.adminToken, s.state))
	mux.HandleFunc("POST /admin/reset", adminOnly(s.adminToken, s.reset))
	return mux
}

func (s *echoServer) protected(w http.ResponseWriter, r *http.Request) {
	last := map[string]string{
		"userId": r.Header.Get("X-User-Id"), "sessionId": r.Header.Get("X-Session-Id"), "tokenId": r.Header.Get("X-Token-Id"),
	}
	s.mu.Lock()
	s.count++
	s.last = last
	count := s.count
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"count": count, "headers": last})
}

func (s *echoServer) state(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"count": s.count, "headers": s.last})
}

func (s *echoServer) reset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.count = 0
	s.last = make(map[string]string)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"reset": true})
}

type controlServer struct {
	adminToken string
	project    string
	dockerURL  string
	client     *http.Client
	allowed    map[string]bool
}

func newControl(adminToken string) (*controlServer, error) {
	project := strings.TrimSpace(os.Getenv("AUTH_E2E_COMPOSE_PROJECT"))
	dockerURL := strings.TrimRight(strings.TrimSpace(os.Getenv("AUTH_E2E_DOCKER_URL")), "/")
	if project == "" || dockerURL == "" {
		return nil, errors.New("Compose project and Docker proxy URL are required")
	}
	return &controlServer{
		adminToken: adminToken, project: project, dockerURL: dockerURL,
		client: &http.Client{Timeout: 10 * time.Second},
		allowed: map[string]bool{
			"postgres": true, "redis": true, "kafka": true, "auth-service": true,
			"auth-worker": true, "auth-provider": true, "auth-test-consumer": true,
		},
	}, nil
}

func (s *controlServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /admin/containers/{service}/{action}", adminOnly(s.adminToken, s.containerAction))
	mux.HandleFunc("POST /admin/redis/delete", adminOnly(s.adminToken, s.redisDelete))
	mux.HandleFunc("POST /admin/redis/pause-writes", adminOnly(s.adminToken, s.redisPauseWrites))
	mux.HandleFunc("GET /admin/redis/status", adminOnly(s.adminToken, s.redisStatus))
	mux.HandleFunc("GET /admin/state/session", adminOnly(s.adminToken, s.sessionState))
	mux.HandleFunc("GET /admin/state/outbox", adminOnly(s.adminToken, s.outboxState))
	mux.HandleFunc("GET /admin/state/outbox/latest", adminOnly(s.adminToken, s.outboxLatest))
	mux.HandleFunc("GET /admin/state/delivery", adminOnly(s.adminToken, s.deliveryState))
	mux.HandleFunc("GET /admin/state/consumer", adminOnly(s.adminToken, s.consumerState))
	mux.HandleFunc("GET /admin/state/echo", adminOnly(s.adminToken, s.echoState))
	return mux
}

func (s *controlServer) containerAction(w http.ResponseWriter, r *http.Request) {
	service, action := r.PathValue("service"), r.PathValue("action")
	if !s.allowed[service] || (action != "pause" && action != "unpause" && action != "restart" && action != "stop" && action != "start") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "action_not_allowed"})
		return
	}
	id, err := s.findContainer(r.Context(), service)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "container_not_found"})
		return
	}
	path := "/containers/" + url.PathEscape(id) + "/" + action
	query := ""
	if action == "restart" || action == "stop" {
		query = "?t=5"
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.dockerURL+path+query, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "control_failed"})
		return
	}
	response, err := s.client.Do(request)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "control_unavailable"})
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "control_rejected"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"applied": true})
}

func (s *controlServer) findContainer(ctx context.Context, service string) (string, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {
		"com.docker.compose.project=" + s.project,
		"com.docker.compose.service=" + service,
	}})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.dockerURL+"/containers/json?all=1&filters="+url.QueryEscape(string(filters)), nil)
	if err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		return "", errors.New("request cancelled")
	default:
	}
	response, err := s.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var containers []struct {
		ID string `json:"Id"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&containers) != nil || len(containers) != 1 {
		return "", errors.New("container lookup failed")
	}
	return containers[0].ID, nil
}

func (s *controlServer) redisDelete(w http.ResponseWriter, r *http.Request) {
	var request struct {
		SessionID string `json:"sessionId"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if !uuidPattern.MatchString(request.SessionID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_session"})
		return
	}
	if _, err := redisCommand("DEL", sessionStatusKeyPrefix+request.SessionID); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "redis_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *controlServer) redisPauseWrites(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DelayMillis int `json:"delayMillis"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.DelayMillis < 1 || request.DelayMillis > 3000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_delay"})
		return
	}
	if _, err := redisCommand("CLIENT", "PAUSE", strconv.Itoa(request.DelayMillis), "WRITE"); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "redis_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"paused": true})
}

func (s *controlServer) redisStatus(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	if !uuidPattern.MatchString(sessionID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_session"})
		return
	}
	result, err := redisCommand("EXISTS", sessionStatusKeyPrefix+sessionID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "redis_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"exists": result == "1"})
}

func redisCommand(arguments ...string) (string, error) {
	connection, err := net.DialTimeout("tcp", "redis:6379", 500*time.Millisecond)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	var command strings.Builder
	_, _ = fmt.Fprintf(&command, "*%d\r\n", len(arguments))
	for _, argument := range arguments {
		_, _ = fmt.Fprintf(&command, "$%d\r\n%s\r\n", len(argument), argument)
	}
	if _, err := io.WriteString(connection, command.String()); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil || len(line) < 3 || line[0] == '-' {
		return "", errors.New("Redis command failed")
	}
	return strings.TrimSpace(line[1:]), nil
}

func (s *controlServer) sessionState(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	if !uuidPattern.MatchString(sessionID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_session"})
		return
	}
	query := "SELECT session_status FROM auth_sessions WHERE session_id='" + sessionID + "'::uuid"
	output, err := s.postgresQuery(r, query)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "database_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": strings.TrimSpace(output)})
}

func (s *controlServer) outboxState(w http.ResponseWriter, r *http.Request) {
	query := "SELECT publish_status||':'||count(*) FROM auth_outbox_events GROUP BY publish_status ORDER BY publish_status"
	output, err := s.postgresQuery(r, query)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "database_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": parseCounts(output)})
}

func (s *controlServer) outboxLatest(w http.ResponseWriter, r *http.Request) {
	query := "SELECT event_id||'|'||publish_status||'|'||publish_attempts FROM auth_outbox_events"
	correlationID := r.URL.Query().Get("correlationId")
	if correlationID != "" {
		if !uuidPattern.MatchString(correlationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_correlation"})
			return
		}
		query += " WHERE correlation_id='" + correlationID + "'::uuid"
	}
	query += " ORDER BY occurred_at DESC,event_id DESC LIMIT 1"
	output, err := s.postgresQuery(r, query)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "database_unavailable"})
		return
	}
	parts := strings.Split(strings.TrimSpace(output), "|")
	if len(parts) != 3 || !uuidPattern.MatchString(parts[0]) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "event_not_found"})
		return
	}
	attempts, _ := strconv.Atoi(parts[2])
	writeJSON(w, http.StatusOK, map[string]any{"eventId": parts[0], "status": parts[1], "attempts": attempts})
}

func (s *controlServer) deliveryState(w http.ResponseWriter, r *http.Request) {
	query := "SELECT delivery_status||':'||count(*) FROM auth_verification_delivery_payloads GROUP BY delivery_status ORDER BY delivery_status"
	output, err := s.postgresQuery(r, query)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "database_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": parseCounts(output)})
}

func parseCounts(output string) map[string]int {
	result := make(map[string]int)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) == 2 {
			if value, err := strconv.Atoi(parts[1]); err == nil {
				result[parts[0]] = value
			}
		}
	}
	return result
}

func (s *controlServer) postgresQuery(r *http.Request, query string) (string, error) {
	return s.dockerExec(r, "postgres", []string{"psql", "-U", "app", "-d", "auth_service", "-At", "-v", "ON_ERROR_STOP=1", "-c", query})
}

func (s *controlServer) dockerExec(r *http.Request, service string, command []string) (string, error) {
	id, err := s.findContainer(r.Context(), service)
	if err != nil {
		return "", err
	}
	payload, _ := json.Marshal(map[string]any{"AttachStdout": true, "AttachStderr": true, "Tty": false, "Cmd": command})
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, s.dockerURL+"/containers/"+url.PathEscape(id)+"/exec", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var created struct {
		ID string `json:"Id"`
	}
	if response.StatusCode != http.StatusCreated || json.NewDecoder(response.Body).Decode(&created) != nil || created.ID == "" {
		return "", errors.New("Docker exec create failed")
	}
	startPayload := strings.NewReader(`{"Detach":false,"Tty":false}`)
	start, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, s.dockerURL+"/exec/"+url.PathEscape(created.ID)+"/start", startPayload)
	start.Header.Set("Content-Type", "application/json")
	result, err := s.client.Do(start)
	if err != nil {
		return "", err
	}
	defer result.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(result.Body, 1<<20))
	if err != nil || result.StatusCode != http.StatusOK {
		return "", errors.New("Docker exec failed")
	}
	output := demultiplexDocker(raw)
	if strings.Contains(output, "ERROR:") || strings.Contains(output, "psql:") {
		return "", errors.New("database query failed")
	}
	return output, nil
}

func demultiplexDocker(raw []byte) string {
	var output bytes.Buffer
	for len(raw) >= 8 {
		length := int(binary.BigEndian.Uint32(raw[4:8]))
		if length < 0 || len(raw) < 8+length {
			return string(raw)
		}
		output.Write(raw[8 : 8+length])
		raw = raw[8+length:]
	}
	if output.Len() == 0 {
		return string(raw)
	}
	return output.String()
}

func (s *controlServer) consumerState(w http.ResponseWriter, r *http.Request) {
	eventID := r.URL.Query().Get("eventId")
	if !uuidPattern.MatchString(eventID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_event"})
		return
	}
	id, err := s.findContainer(r.Context(), "auth-test-consumer")
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "consumer_unavailable"})
		return
	}
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, s.dockerURL+"/containers/"+url.PathEscape(id)+"/logs?stdout=1&stderr=1&tail=all", nil)
	response, err := s.client.Do(request)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "consumer_unavailable"})
		return
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	logs := demultiplexDocker(raw)
	writeJSON(w, http.StatusOK, map[string]int{"deliveries": strings.Count(logs, eventID)})
}

func (s *controlServer) echoState(w http.ResponseWriter, r *http.Request) {
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://protected-echo:8080/admin/state", nil)
	request.Header.Set("Authorization", "Bearer "+s.adminToken)
	response, err := s.client.Do(request)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "echo_unavailable"})
		return
	}
	defer response.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(response.Body, maxBodyBytes))
}
