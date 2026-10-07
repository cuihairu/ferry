// Package cardcode 生成卡密码（《支付设计》§3.2）：crypto/rand + 去易混淆字符集。
package cardcode

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// charset 去掉 0/1/I/L/O 等易混淆字符，32 个正好占 5 bit。
const charset = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// codeLen 单张卡密的字符数，分组为 XXXX-XXXX-XXXX。
const codeLen = 12

// Generate 生成 n 张卡密明文。随机性不足时整批报错，宁可不发不可发弱码。
func Generate(n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	raw := make([]byte, codeLen*n)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("read random: %w", err)
	}
	codes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var b strings.Builder
		for j := 0; j < codeLen; j++ {
			if j > 0 && j%4 == 0 {
				b.WriteByte('-')
			}
			b.WriteByte(charset[int(raw[i*codeLen+j])%len(charset)])
		}
		codes = append(codes, b.String())
	}
	return codes, nil
}
