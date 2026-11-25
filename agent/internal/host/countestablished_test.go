package host

import (
	"strings"
	"testing"
)

// fixture builds lines in raw /proc/net/tcp format: state is column 4 (hexadecimal),
// 01 = ESTABLISHED.
func establishedFixture(v4, v6 string) string {
	return v4 + "\n" + v6
}

func TestCountEstablishedFrom(t *testing.T) {
	v4 := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 0100007F:8AE4 01 00000000:00000000 00:00000000 00000000  1000        0 12345 1
   1: 0100007F:8AE4 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 12346 1
   2: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12347 1
   3: 0100007F:1F90 0100007F:C350 06 00000000:00000000 00:00000000 00000000  1000        0 12348 1
bad line no cols
   4: zz:1F90 0100007F:C350 01 00000000:00000000 00:00000000 00000000  1000        0 12349 1
`
	v6 := `  sl  local_address remote_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode
   0: 00000000000000000000000000000000:0016 00000000000000000000000000000001:8AE4 01 00000000:00000000 00:00000000 00000000 0 0 22345 1
   1: 00000000000000000000000000000001:8AE4 00000000000000000000000000000000:0016 08 00000000:00000000 00:00000000 00000000 0 0 22346 1
`
	got := countEstablishedFrom(strings.NewReader(establishedFixture(v4, v6)))
	// v4: three 01 lines (including bad address line 4) + v6: one 01 line = 4; 0A=LISTEN, 06=TIME_WAIT and 08=CLOSE_WAIT do not count.
	if got != 4 {
		t.Fatalf("count = %d; want 4", got)
	}
}

func TestCountEstablishedFromEmpty(t *testing.T) {
	if got := countEstablishedFrom(strings.NewReader("")); got != 0 {
		t.Fatalf("count = %d; want 0", got)
	}
}
