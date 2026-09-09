package httputil

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestPrereadBodyPreservesOriginalRequestStats(t *testing.T) {
	for _, encoding := range []string{"identity", "gzip", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			var compressed bytes.Buffer
			var writer io.WriteCloser
			switch encoding {
			case "gzip":
				writer = gzip.NewWriter(&compressed)
			case "deflate":
				writer = zlib.NewWriter(&compressed)
			case "zstd":
				encoder, err := zstd.NewWriter(&compressed)
				require.NoError(t, err)
				writer = encoder
			}
			if writer == nil {
				compressed.WriteString(samplePayload)
			} else {
				_, err := io.WriteString(writer, samplePayload)
				require.NoError(t, err)
				require.NoError(t, writer.Close())
			}
			req := newRequestWithBody(t, compressed.Bytes(), encoding)
			decoded, err := ReadRequestBodyWithPrealloc(req) // allowlist middleware
			require.NoError(t, err)
			require.Equal(t, samplePayload, string(decoded))
			req.Body = NewPrereadBody(decoded)
			_, err = io.ReadFull(req.Body, make([]byte, 3)) // partial multipart consumer
			require.NoError(t, err)
			body, stats, err := ReadRequestBodyWithStats(req) // usage handler
			require.NoError(t, err)
			require.Equal(t, &decoded[0], &body[0], "preread must keep the zero-copy fast path")
			require.Equal(t, RequestBodyStats{
				RawBytes: int64(compressed.Len()), DecodedBytes: int64(len(samplePayload)),
				ContentEncoding: encoding, Decoded: encoding != "identity",
			}, stats)
			// Composite routing can replace the JSON without changing the original
			// request's wire size or causing it to be decompressed a second time.
			rewritten := []byte(`{"model":"routed-model","input":"changed by routing"}`)
			req.Body = NewPrereadBody(rewritten)
			body, nextStats, err := ReadRequestBodyWithStats(req)
			require.NoError(t, err)
			require.Equal(t, rewritten, body)
			require.Equal(t, stats, nextStats)
		})
	}
}
