package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tileforge/octoconv-worker-document/internal/convert"
)

func TestProtocolDialReadsDocumentVariables(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")
	ca := filepath.Join(dir, "ca.pem")
	manifest := filepath.Join(dir, "manifest.json")
	for _, path := range []string{cert, key, ca, manifest} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	id := uuid.New()
	t.Setenv("DOCUMENT_PROTOCOL_CORE_URL", "https://core.example")
	t.Setenv("DOCUMENT_PROTOCOL_CLIENT_CERT", cert)
	t.Setenv("DOCUMENT_PROTOCOL_CLIENT_KEY", key)
	t.Setenv("DOCUMENT_PROTOCOL_CLIENT_CA", ca)
	t.Setenv("DOCUMENT_PROTOCOL_WORKER_ID", id.String())
	t.Setenv("DOCUMENT_PROTOCOL_MANIFEST_PATH", manifest)
	t.Setenv("DOCUMENT_WORKER_CONCURRENCY", "")
	t.Setenv("DOCUMENT_ENGINE_TIMEOUT", "90s")
	dial, err := loadProtocolDial()
	if err != nil {
		t.Fatal(err)
	}
	if !dial.ready || dial.workerID != id || dial.freeSlots != 1 || dial.core.Host != "core.example" || dial.engineLimit != 90*time.Second {
		t.Fatalf("dial %+v", dial)
	}
	t.Setenv("DOCUMENT_WORKER_CONCURRENCY", "1 # one slot")
	dial, err = loadProtocolDial()
	if err != nil || dial.freeSlots != 1 {
		t.Fatalf("dial %+v %v", dial, err)
	}
}

func TestProtocolDialRejectsExtraSlots(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")
	ca := filepath.Join(dir, "ca.pem")
	manifest := filepath.Join(dir, "manifest.json")
	for _, path := range []string{cert, key, ca, manifest} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	id := uuid.New()
	t.Setenv("DOCUMENT_PROTOCOL_CORE_URL", "https://core.example")
	t.Setenv("DOCUMENT_PROTOCOL_CLIENT_CERT", cert)
	t.Setenv("DOCUMENT_PROTOCOL_CLIENT_KEY", key)
	t.Setenv("DOCUMENT_PROTOCOL_CLIENT_CA", ca)
	t.Setenv("DOCUMENT_PROTOCOL_WORKER_ID", id.String())
	t.Setenv("DOCUMENT_PROTOCOL_MANIFEST_PATH", manifest)
	for _, value := range []string{"2", "0", "nope"} {
		t.Setenv("DOCUMENT_WORKER_CONCURRENCY", value)
		if _, err := loadProtocolDial(); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}

func TestProtocolDialRejectsPartialEnvironment(t *testing.T) {
	for _, key := range []string{
		"DOCUMENT_PROTOCOL_CORE_URL",
		"DOCUMENT_PROTOCOL_CLIENT_CERT",
		"DOCUMENT_PROTOCOL_CLIENT_KEY",
		"DOCUMENT_PROTOCOL_CLIENT_CA",
		"DOCUMENT_PROTOCOL_WORKER_ID",
		"DOCUMENT_PROTOCOL_MANIFEST_PATH",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("DOCUMENT_PROTOCOL_CORE_URL", "https://core.example")
	if _, err := loadProtocolDial(); err == nil {
		t.Fatal("partial dial")
	}
}

func TestProtocolDialRejectsCleartextAndUppercaseID(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")
	ca := filepath.Join(dir, "ca.pem")
	manifest := filepath.Join(dir, "manifest.json")
	for _, path := range []string{cert, key, ca, manifest} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	id := uuid.New()
	t.Setenv("DOCUMENT_PROTOCOL_CLIENT_CERT", cert)
	t.Setenv("DOCUMENT_PROTOCOL_CLIENT_KEY", key)
	t.Setenv("DOCUMENT_PROTOCOL_CLIENT_CA", ca)
	t.Setenv("DOCUMENT_PROTOCOL_MANIFEST_PATH", manifest)
	t.Setenv("DOCUMENT_PROTOCOL_WORKER_ID", id.String())
	t.Setenv("DOCUMENT_PROTOCOL_CORE_URL", "http://core.example")
	if _, err := loadProtocolDial(); err == nil {
		t.Fatal("http dial")
	}
	t.Setenv("DOCUMENT_PROTOCOL_CORE_URL", "https://core.example")
	t.Setenv("DOCUMENT_PROTOCOL_WORKER_ID", strings.ToUpper(id.String()))
	if _, err := loadProtocolDial(); err == nil {
		t.Fatal("uppercase worker id")
	}
}

func TestRefuseLegacyStores(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_ADDR", "")
	if err := refuseLegacyStores(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", "postgres://db.example/octo")
	if err := refuseLegacyStores(); err == nil {
		t.Fatal("database url")
	}
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_ADDR", "redis.example:6379")
	if err := refuseLegacyStores(); err == nil {
		t.Fatal("redis")
	}
}

func TestManifestOperation(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schema", "manifest-document-convert.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := manifestOperation(raw)
	if err != nil || got != convert.OperationConvert {
		t.Fatalf("operation %q %v", got, err)
	}
	for _, body := range []string{
		`{"capabilities":[{"legacy":{"operation":"convert","engine":"cad"}}]}`,
		`{"capabilities":[{"legacy":{"operation":"render","engine":"document"}}]}`,
		`{"capabilities":[{"legacy":{"operation":"convert","engine":"document"}},{"legacy":{"operation":"convert","engine":"document"}}]}`,
	} {
		if _, err := manifestOperation([]byte(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestModuleDoesNotImportStores(t *testing.T) {
	forbidden := []string{
		"database/sql",
		"github.com/jackc/pgx",
		"github.com/redis/go-redis",
		"github.com/hibiken/asynq",
		"github.com/minio/minio-go",
	}
	set := token.NewFileSet()
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "vendor") {
			return fs.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(set, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported := strings.Trim(spec.Path.Value, `"`)
			for _, bad := range forbidden {
				if imported == bad || strings.HasPrefix(imported, bad+"/") {
					t.Fatalf("%s imports %s", path, imported)
				}
			}
		}
		if entry.Name() != "main.go" {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(body, []byte("DATABASE_URL")) || bytes.Contains(body, []byte("REDIS_ADDR")) {
				t.Fatalf("%s names a legacy store", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAssignmentOutputs(t *testing.T) {
	primary := fileLinkJSON{Role: "primary", Ordinal: 0, Href: "https://files/primary", Format: "pdf"}
	got, ok := assignmentOutputs([]fileLinkJSON{primary})
	if !ok || got.Href != primary.Href {
		t.Fatalf("primary %+v ok=%v", got, ok)
	}
	if _, ok = assignmentOutputs([]fileLinkJSON{primary, {Role: "measurements", Ordinal: 0, Href: "https://files/m", Format: "json"}}); ok {
		t.Fatal("measurements accepted")
	}
	if _, ok = assignmentOutputs([]fileLinkJSON{primary, primary}); ok {
		t.Fatal("two primaries accepted")
	}
	if _, ok = assignmentOutputs([]fileLinkJSON{{Role: "primary", Ordinal: 1, Href: "https://files/primary", Format: "pdf"}}); ok {
		t.Fatal("ordinal accepted")
	}
}

func TestDocumentEngineCode(t *testing.T) {
	if documentEngineCode(context.DeadlineExceeded) != "timeout" {
		t.Fatal("timeout")
	}
	if documentEngineCode(os.ErrNotExist) != "engine_error" {
		t.Fatal("engine")
	}
}

type deadlineTrip struct {
	base http.RoundTripper
	ok   bool
	left time.Duration
}

func (d *deadlineTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	if !d.ok {
		if dl, ok := r.Context().Deadline(); ok {
			d.ok = true
			d.left = time.Until(dl)
		}
	}
	return d.base.RoundTrip(r)
}

func protocolTestClient(t *testing.T, handler http.Handler, trip *deadlineTrip) *protocolClient {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	core, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := srv.Client()
	if trip != nil {
		trip.base = httpClient.Transport
		httpClient.Transport = trip
	}
	return &protocolClient{
		core:     core,
		coreHTTP: httpClient,
		term:     make(chan struct{}),
	}
}

func TestHelloRequestHasDeadline(t *testing.T) {
	trip := &deadlineTrip{}
	c := protocolTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"session_id":"sess","session_token":"token","lease_seconds":30,"pull_wait_seconds":0,"heartbeat_interval_seconds":10}`)
	}), trip)
	if _, err := c.hello(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !trip.ok {
		t.Fatal("hello has no deadline")
	}
	if trip.left > helloTimeout || trip.left < helloTimeout-2*time.Second {
		t.Fatalf("hello deadline %s", trip.left)
	}
}

type fixedConverter struct {
	body []byte
}

func (fixedConverter) Pairs() []convert.Pair { return nil }

func (f fixedConverter) Convert(_ context.Context, _, outPath string, _ map[string]any) error {
	return os.WriteFile(outPath, f.body, 0o600)
}

func TestTwelvePairsPostAnAcceptedComplete(t *testing.T) {
	pairs := (convert.LibreOfficeConverter{}).Pairs()
	if len(pairs) != 12 {
		t.Fatalf("pairs %d", len(pairs))
	}
	var mu sync.Mutex
	puts := map[string]putBody{}
	c := protocolTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte("source"))
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			puts[r.URL.Path] = putBody{body: body, contentType: r.Header.Get("Content-Type")}
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			if strings.Contains(r.URL.Path, "/complete") {
				if r.Header.Get("Octoconv-Attempt") == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				mu.Lock()
				err = acceptDocumentComplete(raw, puts)
				mu.Unlock()
				if err != nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"code":"artifact_rejected"}`))
					return
				}
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}), nil)
	c.fileHTTP = c.coreHTTP
	c.operation = convert.OperationConvert
	c.engineLimit = time.Minute
	c.leaseSeconds = 30
	c.sessionToken = "token"
	for _, pair := range pairs {
		body := []byte(pair.From + "->" + pair.To)
		c.lookupOp = func(op, from, to string) (convert.Converter, bool) {
			if op != convert.OperationConvert || from != pair.From || to != pair.To {
				return nil, false
			}
			return fixedConverter{body: body}, true
		}
		href := c.core.String() + "/objects/" + pair.From + "-" + pair.To
		asgn := assignmentJSON{
			AttemptID:       pair.From + "-" + pair.To,
			AttemptToken:    "attempt-token",
			AttemptDeadline: time.Now().Add(30 * time.Minute).Format(time.RFC3339Nano),
			LeaseExpiresAt:  time.Now().Add(time.Hour).Format(time.RFC3339Nano),
			Limits:          limitsJSON{MaxOutputBytes: convert.MaxFileBytes},
			Inputs:          []fileLinkJSON{{Role: "source", Ordinal: 0, Href: href, Format: pair.From}},
			Outputs:         []fileLinkJSON{{Role: "primary", Ordinal: 0, Href: href, Format: pair.To}},
		}
		if err := c.runAssignment(context.Background(), asgn, controlJSON{}); err != nil {
			t.Fatalf("%s to %s: %v", pair.From, pair.To, err)
		}
		mu.Lock()
		put := puts["/objects/"+pair.From+"-"+pair.To]
		mu.Unlock()
		if !bytes.Equal(put.body, body) {
			t.Fatalf("%s to %s put %q", pair.From, pair.To, put.body)
		}
		if put.contentType != convert.MIMEType(pair.To) {
			t.Fatalf("%s to %s content type %s", pair.From, pair.To, put.contentType)
		}
	}
}

type putBody struct {
	body        []byte
	contentType string
}

func acceptDocumentComplete(raw []byte, puts map[string]putBody) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var body struct {
		ProtocolVersion int `json:"protocol_version"`
		Artifacts       []struct {
			Role        string `json:"role"`
			Ordinal     int    `json:"ordinal"`
			Format      string `json:"format"`
			ContentType string `json:"content_type"`
			SHA256      string `json:"sha256"`
			ByteSize    int64  `json:"byte_size"`
		} `json:"artifacts"`
	}
	if err := dec.Decode(&body); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return os.ErrInvalid
	}
	if body.ProtocolVersion != 1 || len(body.Artifacts) != 1 {
		return os.ErrInvalid
	}
	art := body.Artifacts[0]
	if art.Role != "primary" || art.Ordinal != 0 || art.Format == "" {
		return os.ErrInvalid
	}
	if art.ContentType != convert.MIMEType(art.Format) || art.ByteSize < 0 || art.ByteSize > convert.MaxFileBytes {
		return os.ErrInvalid
	}
	if len(art.SHA256) != 64 || strings.ToLower(art.SHA256) != art.SHA256 {
		return os.ErrInvalid
	}
	var matched bool
	for _, put := range puts {
		sum := sha256.Sum256(put.body)
		if hex.EncodeToString(sum[:]) != art.SHA256 {
			continue
		}
		if put.contentType != art.ContentType || int64(len(put.body)) != art.ByteSize {
			return os.ErrInvalid
		}
		matched = true
	}
	if !matched {
		return os.ErrInvalid
	}
	return nil
}

func TestAttemptDeadlineStopsStalledDownload(t *testing.T) {
	failed := make(chan []byte, 1)
	c := protocolTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			<-r.Context().Done()
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(r.URL.Path, "/fail") {
			failed <- raw
		}
		w.WriteHeader(http.StatusNoContent)
	}), nil)
	c.fileHTTP = c.coreHTTP
	c.operation = convert.OperationConvert
	c.engineLimit = time.Minute
	href := c.core.String() + "/objects"
	asgn := assignmentJSON{
		AttemptID:       "attempt",
		AttemptToken:    "tok",
		AttemptDeadline: time.Now().Add(200 * time.Millisecond).Format(time.RFC3339Nano),
		LeaseExpiresAt:  time.Now().Add(time.Hour).Format(time.RFC3339Nano),
		Inputs:          []fileLinkJSON{{Role: "source", Ordinal: 0, Href: href, Format: "docx"}},
		Outputs:         []fileLinkJSON{{Role: "primary", Ordinal: 0, Href: href, Format: "pdf"}},
	}
	done := make(chan error, 1)
	go func() {
		done <- c.runAssignment(context.Background(), asgn, controlJSON{})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attempt ignored the deadline")
	}
	select {
	case body := <-failed:
		if !strings.Contains(string(body), `"engine_code":"timeout"`) {
			t.Fatalf("fail body %s", body)
		}
	default:
		t.Fatal("fail was not posted")
	}
}
