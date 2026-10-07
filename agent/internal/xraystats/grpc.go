// 极简 gRPC unary 客户端与 proto3 手写编解码，只为 QueryStats 一条调用存在。
// 为什么不用 google.golang.org/grpc：完整客户端会把核心二进制顶到 10MB 预算外
// （E-4 实测 +8.3MB），而这里只调 xray 的一个方法。走 h2c 明文（xray api
// 只暴露在 127.0.0.1），HTTP/2 由 golang.org/x/net/http2 承担，消息体按
// stats.proto 的字段号手写 varint 编解码。
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

// grpcUnary 对 addr 发起一次 gRPC unary 调用，返回首条响应消息的净载荷
// （已剥 5 字节 gRPC 分帧头）。错误含 gRPC status 非 0 与传输失败两类。
func grpcUnary(ctx context.Context, addr, method string, req []byte) ([]byte, error) {
	t := &http2.Transport{
		AllowHTTP: true, // h2c 明文先验连接
		DialTLS: func(network, a string, _ *tls.Config) (net.Conn, error) {
			return net.Dial(network, a)
		},
	}
	// gRPC 分帧：1 字节压缩标志（恒 0）+ 4 字节大端长度 + 消息体
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
	// gRPC 应答的 grpc-status 恒在 trailer；读完 body 后才可见
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

// grpcUnescape 解 gRPC percent-encoding 的 Grpc-Message（%AB 十六进制）。
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

// ---- proto3 手写编解码（字段号见 stats.proto）----

// pbAppendVarint 追加一个 varint 字段（wire type 0）。
func pbAppendVarint(b []byte, field uint32, v uint64) []byte {
	b = binary.AppendUvarint(b, uint64(field)<<3|0)
	return binary.AppendUvarint(b, v)
}

// pbAppendBytes 追加一个 length-delimited 字段（wire type 2）。
func pbAppendBytes(b []byte, field uint32, v []byte) []byte {
	b = binary.AppendUvarint(b, uint64(field)<<3|2)
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}

// pbConsumeTag 读字段头，返回 field 号、wire type 与剩余体。
func pbConsumeTag(b []byte) (field uint32, wire uint64, rest []byte, err error) {
	tag, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, 0, nil, errors.New("bad proto tag")
	}
	return uint32(tag >> 3), tag & 7, b[n:], nil
}

// pbConsumeVarint 读一个 varint 值。
func pbConsumeVarint(b []byte) (uint64, []byte, error) {
	v, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, nil, errors.New("bad proto varint")
	}
	return v, b[n:], nil
}

// pbSkip 跳过一个非关注字段体。
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

// decodeStat 解 Stat{1:name, 2:value}。
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

// decodeQueryStatsResp 解 QueryStatsResponse{1: repeated Stat}。
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

// encodeQueryStatsReq 编 QueryStatsRequest{1:pattern, 2:reset}。
func encodeQueryStatsReq(pattern string, reset bool) []byte {
	var b []byte
	b = pbAppendBytes(b, 1, []byte(pattern))
	if reset {
		b = pbAppendVarint(b, 2, 1)
	}
	return b
}
