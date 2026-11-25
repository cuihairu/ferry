// A minimal gRPC unary client with hand-written proto3 codec, existing for one call only: QueryStats.
// Why not google.golang.org/grpc: the full client would push the core binary past the 10MB budget
// (E-4 measured +8.3MB), while this only calls one xray method. It goes over h2c plaintext (the xray api
// is exposed on 127.0.0.1 only), HTTP/2 is carried by golang.org/x/net/http2, and message bodies are
// hand-coded as varints per the field numbers of stats.proto.
package xraystats

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/http2"
)

// grpcUnary makes one gRPC unary call to addr and returns the clean payload of the first response message
// (the 5-byte gRPC framing header already stripped). Errors cover both a non-zero gRPC status and transport failures.
func grpcUnary(ctx context.Context, addr, method string, req []byte) ([]byte, error) {
	t := &http2.Transport{
		AllowHTTP: true, // verify the connection over h2c plaintext first
		DialTLS: func(network, a string, _ *tls.Config) (net.Conn, error) {
			return net.Dial(network, a)
		},
	}
	// gRPC framing: 1 byte compression flag (always 0) + 4 bytes big-endian length + the message body
	frame := make([]byte, 5+len(req))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(req)))
	copy(frame[5:], req)

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+method, bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("content-type", "application/grpc+proto")
	hreq.Header.Set("te", "trailers")

	resp, err := t.RoundTrip(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("grpc http status %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// the grpc-status of a gRPC reply always lives in the trailer, visible only after the body has been read
	if st := resp.Trailer.Get("Grpc-Status"); st != "" && st != "0" {
		return nil, fmt.Errorf("grpc status %s: %s", st, grpcUnescape(resp.Trailer.Get("Grpc-Message")))
	}
	if len(payload) < 5 {
		return nil, errors.New("grpc body too short")
	}
	n := binary.BigEndian.Uint32(payload[1:5])
	if uint32(len(payload)-5) < n {
		return nil, errors.New("grpc body truncated")
	}
	return payload[5 : 5+n], nil
}

// grpcUnescape decodes the gRPC percent-encoding of Grpc-Message (%AB hexadecimal).
func grpcUnescape(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				out = append(out, byte(v))
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return string(out)
}

// ---- hand-written proto3 codec (field numbers per stats.proto) ----

// pbAppendVarint appends a varint field (wire type 0).
func pbAppendVarint(b []byte, field uint32, v uint64) []byte {
	b = binary.AppendUvarint(b, uint64(field)<<3|0)
	return binary.AppendUvarint(b, v)
}

// pbAppendBytes appends a length-delimited field (wire type 2).
func pbAppendBytes(b []byte, field uint32, v []byte) []byte {
	b = binary.AppendUvarint(b, uint64(field)<<3|2)
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}

// pbConsumeTag reads a field header and returns the field number, wire type and the remaining body.
func pbConsumeTag(b []byte) (field uint32, wire uint64, rest []byte, err error) {
	tag, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, 0, nil, errors.New("bad proto tag")
	}
	return uint32(tag >> 3), tag & 7, b[n:], nil
}

// pbConsumeVarint reads one varint value.
func pbConsumeVarint(b []byte) (uint64, []byte, error) {
	v, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, nil, errors.New("bad proto varint")
	}
	return v, b[n:], nil
}

// pbSkip skips the body of a field we do not care about.
func pbSkip(b []byte, wire uint64) ([]byte, error) {
	switch wire {
	case 0:
		_, rest, err := pbConsumeVarint(b)
		return rest, err
	case 1:
		if len(b) < 8 {
			return nil, errors.New("bad proto fixed64")
		}
		return b[8:], nil
	case 2:
		l, rest, err := pbConsumeVarint(b)
		if err != nil {
			return nil, err
		}
		if uint64(len(rest)) < l {
			return nil, errors.New("bad proto length")
		}
		return rest[l:], nil
	case 5:
		if len(b) < 4 {
			return nil, errors.New("bad proto fixed32")
		}
		return b[4:], nil
	default:
		return nil, fmt.Errorf("unsupported proto wire %d", wire)
	}
}

// decodeStat parses Stat{1:name, 2:value}.
func decodeStat(b []byte) (Stat, error) {
	var s Stat
	for len(b) > 0 {
		field, wire, rest, err := pbConsumeTag(b)
		if err != nil {
			return s, err
		}
		switch {
		case field == 1 && wire == 2:
			l, r, err := pbConsumeVarint(rest)
			if err != nil {
				return s, err
			}
			if uint64(len(r)) < l {
				return s, errors.New("bad proto name")
			}
			s.Name = string(r[:l])
			b = r[l:]
		case field == 2 && wire == 0:
			v, r, err := pbConsumeVarint(rest)
			if err != nil {
				return s, err
			}
			s.Value = v
			b = r
		default:
			if b, err = pbSkip(rest, wire); err != nil {
				return s, err
			}
		}
	}
	return s, nil
}

// decodeQueryStatsResp parses QueryStatsResponse{1: repeated Stat}.
func decodeQueryStatsResp(b []byte) ([]Stat, error) {
	var out []Stat
	for len(b) > 0 {
		field, wire, rest, err := pbConsumeTag(b)
		if err != nil {
			return nil, err
		}
		if field == 1 && wire == 2 {
			l, r, err := pbConsumeVarint(rest)
			if err != nil {
				return nil, err
			}
			if uint64(len(r)) < l {
				return nil, errors.New("bad proto stat")
			}
			s, err := decodeStat(r[:l])
			if err != nil {
				return nil, err
			}
			out = append(out, s)
			b = r[l:]
			continue
		}
		if b, err = pbSkip(rest, wire); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// encodeQueryStatsReq encodes QueryStatsRequest{1:pattern, 2:reset}.
func encodeQueryStatsReq(pattern string, reset bool) []byte {
	var b []byte
	b = pbAppendBytes(b, 1, []byte(pattern))
	if reset {
		b = pbAppendVarint(b, 2, 1)
	}
	return b
}
