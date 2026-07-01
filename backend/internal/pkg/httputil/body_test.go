package httputil

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"net/http"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

const samplePayload = `{"model":"gpt-5.5","input":"hi","stream":false}`

func newRequestWithBody(t *testing.T, body []byte, encoding string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if encoding != "" {
		req.Header.Set("Content-Encoding", encoding)
	}
	req.ContentLength = int64(len(body))
	return req
}

func TestReadRequestBodyWithPrealloc_PassesThroughIdentity(t *testing.T) {
	req := newRequestWithBody(t, []byte(samplePayload), "")
	got, err := ReadRequestBodyWithPrealloc(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != samplePayload {
		t.Fatalf("body mismatch: got %q", got)
	}
}

func TestReadRequestBodyWithStats_PassesThroughIdentity(t *testing.T) {
	req := newRequestWithBody(t, []byte(samplePayload), "")
	got, stats, err := ReadRequestBodyWithStats(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != samplePayload {
		t.Fatalf("body mismatch: got %q", got)
	}
	if stats.RawBytes != int64(len(samplePayload)) {
		t.Fatalf("raw bytes mismatch: got %d", stats.RawBytes)
	}
	if stats.DecodedBytes != int64(len(samplePayload)) {
		t.Fatalf("decoded bytes mismatch: got %d", stats.DecodedBytes)
	}
	if stats.Decoded {
		t.Fatal("identity body should not be marked decoded")
	}
}

func TestReadRequestBodyWithPrealloc_DecodesZstd(t *testing.T) {
	enc, _ := zstd.NewWriter(nil)
	compressed := enc.EncodeAll([]byte(samplePayload), nil)
	_ = enc.Close()

	req := newRequestWithBody(t, compressed, "zstd")
	got, err := ReadRequestBodyWithPrealloc(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != samplePayload {
		t.Fatalf("body mismatch: got %q", got)
	}
	if req.Header.Get("Content-Encoding") != "" {
		t.Fatalf("Content-Encoding should be cleared after decoding")
	}
	if req.ContentLength != int64(len(samplePayload)) {
		t.Fatalf("ContentLength not updated: %d", req.ContentLength)
	}
}

func TestReadRequestBodyWithStats_DecodesZstd(t *testing.T) {
	enc, _ := zstd.NewWriter(nil)
	compressed := enc.EncodeAll([]byte(samplePayload), nil)
	_ = enc.Close()

	req := newRequestWithBody(t, compressed, "zstd")
	got, stats, err := ReadRequestBodyWithStats(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != samplePayload {
		t.Fatalf("body mismatch: got %q", got)
	}
	if stats.RawBytes != int64(len(compressed)) {
		t.Fatalf("raw bytes mismatch: got %d", stats.RawBytes)
	}
	if stats.DecodedBytes != int64(len(samplePayload)) {
		t.Fatalf("decoded bytes mismatch: got %d", stats.DecodedBytes)
	}
	if !stats.Decoded {
		t.Fatal("compressed body should be marked decoded")
	}
	if stats.ContentEncoding != "zstd" {
		t.Fatalf("encoding mismatch: got %q", stats.ContentEncoding)
	}
}

func TestReadRequestBodyWithPrealloc_DecodesGzip(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write([]byte(samplePayload)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	req := newRequestWithBody(t, buf.Bytes(), "gzip")
	got, err := ReadRequestBodyWithPrealloc(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != samplePayload {
		t.Fatalf("body mismatch: got %q", got)
	}
}

func TestReadRequestBodyWithPrealloc_DecodesDeflate(t *testing.T) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write([]byte(samplePayload)); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}

	req := newRequestWithBody(t, buf.Bytes(), "deflate")
	got, err := ReadRequestBodyWithPrealloc(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != samplePayload {
		t.Fatalf("body mismatch: got %q", got)
	}
}

func TestReadRequestBodyWithPrealloc_RejectsUnsupportedEncoding(t *testing.T) {
	req := newRequestWithBody(t, []byte(samplePayload), "br")
	_, err := ReadRequestBodyWithPrealloc(req)
	if err == nil {
		t.Fatal("expected error for unsupported encoding, got nil")
	}
	if !strings.Contains(err.Error(), "br") {
		t.Fatalf("error should mention encoding, got %v", err)
	}
}

func TestReadRequestBodyWithPrealloc_RejectsCorruptZstd(t *testing.T) {
	req := newRequestWithBody(t, []byte("not actually zstd"), "zstd")
	_, err := ReadRequestBodyWithPrealloc(req)
	if err == nil {
		t.Fatal("expected error for corrupt zstd body, got nil")
	}
}

func TestReadRequestBodyWithPrealloc_NilBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/v1/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	got, err := ReadRequestBodyWithPrealloc(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil body, got %q", got)
	}
}

func TestReadRequestBodyWithStats_NilBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/v1/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	got, stats, err := ReadRequestBodyWithStats(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil body, got %q", got)
	}
	if stats.RawBytes != 0 || stats.DecodedBytes != 0 {
		t.Fatalf("expected zero stats, got %+v", stats)
	}
}

func TestReadRequestBodyWithPrealloc_RespectsIdentityEncoding(t *testing.T) {
	req := newRequestWithBody(t, []byte(samplePayload), "identity")
	got, err := ReadRequestBodyWithPrealloc(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != samplePayload {
		t.Fatalf("body mismatch: got %q", got)
	}
}

func BenchmarkReadRequestBodyWithStats_Identity_4MB(b *testing.B) {
	benchmarkReadRequestBodyWithStatsIdentity(b, 4<<20)
}

func BenchmarkReadRequestBodyWithStats_Identity_16MB(b *testing.B) {
	benchmarkReadRequestBodyWithStatsIdentity(b, 16<<20)
}

func BenchmarkReadRequestBodyWithStats_Identity_32MB(b *testing.B) {
	benchmarkReadRequestBodyWithStatsIdentity(b, 32<<20)
}

func BenchmarkReadRequestBodyWithStats_Zstd_4MB(b *testing.B) {
	benchmarkReadRequestBodyWithStatsZstd(b, 4<<20)
}

func BenchmarkReadRequestBodyWithStats_Zstd_16MB(b *testing.B) {
	benchmarkReadRequestBodyWithStatsZstd(b, 16<<20)
}

func BenchmarkReadRequestBodyWithStats_Zstd_32MB(b *testing.B) {
	benchmarkReadRequestBodyWithStatsZstd(b, 32<<20)
}

func benchmarkReadRequestBodyWithStatsIdentity(b *testing.B, size int) {
	payload := bytes.Repeat([]byte("a"), size)
	b.SetBytes(int64(size))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req := newBenchmarkRequest(payload, "")
		got, stats, err := ReadRequestBodyWithStats(req)
		if err != nil {
			b.Fatalf("ReadRequestBodyWithStats: %v", err)
		}
		if len(got) != size || stats.DecodedBytes != int64(size) {
			b.Fatalf("unexpected body/stats size: got=%d stats=%+v", len(got), stats)
		}
	}
}

func benchmarkReadRequestBodyWithStatsZstd(b *testing.B, size int) {
	payload := bytes.Repeat([]byte("a"), size)
	enc, _ := zstd.NewWriter(nil)
	compressed := enc.EncodeAll(payload, nil)
	_ = enc.Close()
	b.SetBytes(int64(size))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req := newBenchmarkRequest(compressed, "zstd")
		got, stats, err := ReadRequestBodyWithStats(req)
		if err != nil {
			b.Fatalf("ReadRequestBodyWithStats: %v", err)
		}
		if len(got) != size || stats.DecodedBytes != int64(size) || stats.RawBytes != int64(len(compressed)) {
			b.Fatalf("unexpected body/stats size: got=%d stats=%+v", len(got), stats)
		}
	}
}

func newBenchmarkRequest(body []byte, encoding string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	if encoding != "" {
		req.Header.Set("Content-Encoding", encoding)
	}
	req.ContentLength = int64(len(body))
	return req
}
