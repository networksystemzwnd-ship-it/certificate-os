// Package qr is a small QR Code (Model 2) encoder: byte mode, error correction level M, versions 1–10
// (up to 213 bytes). It exists so certificates can carry a verification QR without a third-party
// dependency. Output is verified in tests with an independent decoder (see qr_test.go).
package qr

import (
	"errors"
	"strings"
)

// ErrTooLong is returned when the text does not fit version 10 at level M.
var ErrTooLong = errors.New("qr: text too long")

// MaxBytes is the largest payload Encode accepts.
const MaxBytes = 213

type ver struct {
	ecPerBlock int
	g1n, g1d   int // group 1: blocks, data codewords per block
	g2n, g2d   int
	align      []int
	remainder  int
}

// Level M parameters for versions 1..10 (ISO/IEC 18004).
var versions = [...]ver{
	{},
	{10, 1, 16, 0, 0, nil, 0},
	{16, 1, 28, 0, 0, []int{6, 18}, 7},
	{26, 1, 44, 0, 0, []int{6, 22}, 7},
	{18, 2, 32, 0, 0, []int{6, 26}, 7},
	{24, 2, 43, 0, 0, []int{6, 30}, 7},
	{16, 4, 27, 0, 0, []int{6, 34}, 7},
	{18, 4, 31, 0, 0, []int{6, 22, 38}, 0},
	{22, 2, 38, 2, 39, []int{6, 24, 42}, 0},
	{22, 3, 36, 2, 37, []int{6, 26, 46}, 0},
	{26, 4, 43, 1, 44, []int{6, 28, 50}, 0},
}

func (v ver) dataCodewords() int { return v.g1n*v.g1d + v.g2n*v.g2d }

// --- Reed–Solomon over GF(256), polynomial 0x11D ---

var expT [512]byte
var logT [256]int

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		expT[i] = byte(x)
		logT[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		expT[i] = expT[i-255]
	}
}

func gmul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return expT[logT[a]+logT[b]]
}

func generator(n int) []byte {
	g := []byte{1}
	for i := 0; i < n; i++ {
		next := make([]byte, len(g)+1)
		for j, c := range g {
			next[j] ^= c
			next[j+1] ^= gmul(c, expT[i])
		}
		g = next
	}
	return g
}

func rsRemainder(data []byte, n int) []byte {
	g := generator(n)
	rem := make([]byte, n)
	for _, d := range data {
		f := d ^ rem[0]
		copy(rem, rem[1:])
		rem[n-1] = 0
		for i := 0; i < n; i++ {
			rem[i] ^= gmul(g[i+1], f)
		}
	}
	return rem
}

// --- bit buffer ---

type bits []bool

func (b *bits) add(val, n int) {
	for i := n - 1; i >= 0; i-- {
		*b = append(*b, val>>uint(i)&1 == 1)
	}
}

func pickVersion(n int) (int, error) {
	for v := 1; v <= 10; v++ {
		// 4 mode bits + 8 (v1–9) or 16 (v10+) count bits + 8n data bits.
		cc := 8
		if v >= 10 {
			cc = 16
		}
		if 4+cc+8*n <= versions[v].dataCodewords()*8 {
			return v, nil
		}
	}
	return 0, ErrTooLong
}

func dataCodewords(text []byte, v int) []byte {
	cc := 8
	if v >= 10 {
		cc = 16
	}
	var b bits
	b.add(0b0100, 4)
	b.add(len(text), cc)
	for _, c := range text {
		b.add(int(c), 8)
	}
	total := versions[v].dataCodewords() * 8
	for t := 0; t < 4 && len(b) < total; t++ {
		b = append(b, false)
	}
	for len(b)%8 != 0 {
		b = append(b, false)
	}
	out := make([]byte, 0, total/8)
	for i := 0; i < len(b); i += 8 {
		var x byte
		for j := 0; j < 8; j++ {
			x <<= 1
			if b[i+j] {
				x |= 1
			}
		}
		out = append(out, x)
	}
	for pad := byte(0xEC); len(out) < total/8; pad ^= 0xEC ^ 0x11 {
		out = append(out, pad)
	}
	return out
}

// interleave splits data into blocks, appends RS codewords and interleaves everything.
func interleave(data []byte, v ver) []byte {
	var blocks, ecs [][]byte
	pos := 0
	for i := 0; i < v.g1n+v.g2n; i++ {
		n := v.g1d
		if i >= v.g1n {
			n = v.g2d
		}
		blk := data[pos : pos+n]
		pos += n
		blocks = append(blocks, blk)
		ecs = append(ecs, rsRemainder(blk, v.ecPerBlock))
	}
	var out []byte
	maxD := v.g1d
	if v.g2d > maxD {
		maxD = v.g2d
	}
	for i := 0; i < maxD; i++ {
		for _, b := range blocks {
			if i < len(b) {
				out = append(out, b[i])
			}
		}
	}
	for i := 0; i < v.ecPerBlock; i++ {
		for _, e := range ecs {
			out = append(out, e[i])
		}
	}
	return out
}

// --- matrix ---

type matrix struct {
	n        int
	mod      [][]bool
	reserved [][]bool
}

func newMatrix(n int) *matrix {
	m := &matrix{n: n}
	m.mod = make([][]bool, n)
	m.reserved = make([][]bool, n)
	for i := range m.mod {
		m.mod[i] = make([]bool, n)
		m.reserved[i] = make([]bool, n)
	}
	return m
}

func (m *matrix) set(x, y int, dark bool) { m.mod[y][x] = dark; m.reserved[y][x] = true }

func (m *matrix) finder(cx, cy int) {
	for dy := -1; dy <= 7; dy++ {
		for dx := -1; dx <= 7; dx++ {
			x, y := cx+dx, cy+dy
			if x < 0 || y < 0 || x >= m.n || y >= m.n {
				continue
			}
			dark := dx >= 0 && dx <= 6 && dy >= 0 && dy <= 6 &&
				(dx == 0 || dx == 6 || dy == 0 || dy == 6 || (dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4))
			m.set(x, y, dark)
		}
	}
}

func (m *matrix) alignment(cx, cy int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			d := dx
			if d < 0 {
				d = -d
			}
			e := dy
			if e < 0 {
				e = -e
			}
			m.set(cx+dx, cy+dy, d == 2 || e == 2 || (d == 0 && e == 0))
		}
	}
}

func (m *matrix) function(v int) {
	n := m.n
	m.finder(0, 0)
	m.finder(n-7, 0)
	m.finder(0, n-7)
	for i := 8; i < n-8; i++ { // timing patterns
		m.set(i, 6, i%2 == 0)
		m.set(6, i, i%2 == 0)
	}
	pos := versions[v].align
	for i, ay := range pos {
		for j, ax := range pos {
			if (i == 0 && j == 0) || (i == 0 && j == len(pos)-1) || (i == len(pos)-1 && j == 0) {
				continue
			}
			m.alignment(ax, ay)
		}
	}
	// reserve format information areas and the dark module
	for i := 0; i < 9; i++ {
		m.reserved[8][i], m.reserved[i][8] = true, true
	}
	for i := 0; i < 8; i++ {
		m.reserved[8][n-1-i], m.reserved[n-1-i][8] = true, true
	}
	m.set(8, n-8, true)
	if v >= 7 {
		for i := 0; i < 6; i++ {
			for j := 0; j < 3; j++ {
				m.reserved[i][n-11+j], m.reserved[n-11+j][i] = true, true
			}
		}
	}
}

func (m *matrix) place(data []byte, v int) {
	n := m.n
	var stream []bool
	for _, b := range data {
		for i := 7; i >= 0; i-- {
			stream = append(stream, b>>uint(i)&1 == 1)
		}
	}
	for i := 0; i < versions[v].remainder; i++ {
		stream = append(stream, false)
	}
	k := 0
	up := true
	for x := n - 1; x >= 1; x -= 2 {
		if x == 6 {
			x--
		}
		for i := 0; i < n; i++ {
			y := i
			if up {
				y = n - 1 - i
			}
			for dx := 0; dx < 2; dx++ {
				xx := x - dx
				if m.reserved[y][xx] {
					continue
				}
				if k < len(stream) {
					m.mod[y][xx] = stream[k]
				}
				k++
			}
		}
		up = !up
	}
}

func maskFn(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (y/2+x/3)%2 == 0
	case 5:
		return x*y%2+x*y%3 == 0
	case 6:
		return (x*y%2+x*y%3)%2 == 0
	}
	return ((x+y)%2+x*y%3)%2 == 0
}

func (m *matrix) clone() *matrix {
	c := newMatrix(m.n)
	for y := range m.mod {
		copy(c.mod[y], m.mod[y])
		copy(c.reserved[y], m.reserved[y])
	}
	return c
}

func (m *matrix) applyMask(mask int) {
	for y := 0; y < m.n; y++ {
		for x := 0; x < m.n; x++ {
			if !m.reserved[y][x] && maskFn(mask, x, y) {
				m.mod[y][x] = !m.mod[y][x]
			}
		}
	}
}

func bch(val, poly, valBits, totalBits int) int {
	v := val << uint(totalBits-valBits)
	deg := func(x int) int {
		d := -1
		for ; x > 0; x >>= 1 {
			d++
		}
		return d
	}
	pd := deg(poly)
	for deg(v) >= pd {
		v ^= poly << uint(deg(v)-pd)
	}
	return val<<uint(totalBits-valBits) | v
}

func (m *matrix) writeFormat(mask int) {
	n := m.n
	f := bch(0<<3|mask, 0x537, 5, 15) ^ 0x5412 // level M = 00
	bit := func(i int) bool { return f>>uint(i)&1 == 1 }
	for i := 0; i <= 5; i++ {
		m.mod[i][8] = bit(i)
	}
	m.mod[7][8], m.mod[8][8], m.mod[8][7] = bit(6), bit(7), bit(8)
	for i := 9; i < 15; i++ {
		m.mod[8][14-i] = bit(i)
	}
	for i := 0; i < 8; i++ {
		m.mod[8][n-1-i] = bit(i)
	}
	for i := 8; i < 15; i++ {
		m.mod[n-15+i][8] = bit(i)
	}
	m.mod[n-8][8] = true
}

func (m *matrix) writeVersion(v int) {
	if v < 7 {
		return
	}
	info := bch(v, 0x1F25, 6, 18)
	for i := 0; i < 18; i++ {
		b := info>>uint(i)&1 == 1
		a, c := i/3, m.n-11+i%3
		m.mod[a][c] = b
		m.mod[c][a] = b
	}
}

func penalty(m *matrix) int {
	n := m.n
	p := 0
	runs := func(get func(i, j int) bool) {
		for i := 0; i < n; i++ {
			run := 1
			for j := 1; j < n; j++ {
				if get(i, j) == get(i, j-1) {
					run++
					continue
				}
				if run >= 5 {
					p += run - 2
				}
				run = 1
			}
			if run >= 5 {
				p += run - 2
			}
		}
	}
	runs(func(i, j int) bool { return m.mod[i][j] })
	runs(func(i, j int) bool { return m.mod[j][i] })
	for y := 0; y < n-1; y++ {
		for x := 0; x < n-1; x++ {
			c := m.mod[y][x]
			if c == m.mod[y][x+1] && c == m.mod[y+1][x] && c == m.mod[y+1][x+1] {
				p += 3
			}
		}
	}
	pat1 := []bool{true, false, true, true, true, false, true, false, false, false, false}
	pat2 := []bool{false, false, false, false, true, false, true, true, true, false, true}
	match := func(get func(k int) bool, pat []bool) bool {
		for k, b := range pat {
			if get(k) != b {
				return false
			}
		}
		return true
	}
	for i := 0; i < n; i++ {
		for j := 0; j+11 <= n; j++ {
			row := func(k int) bool { return m.mod[i][j+k] }
			col := func(k int) bool { return m.mod[j+k][i] }
			if match(row, pat1) || match(row, pat2) {
				p += 40
			}
			if match(col, pat1) || match(col, pat2) {
				p += 40
			}
		}
	}
	dark := 0
	for y := range m.mod {
		for _, b := range m.mod[y] {
			if b {
				dark++
			}
		}
	}
	k := dark*100/(n*n) - 50
	if k < 0 {
		k = -k
	}
	p += (k / 5) * 10
	return p
}

// Encode returns the module matrix (true = dark) WITHOUT the quiet zone.
func Encode(text string) ([][]bool, error) {
	b := []byte(text)
	if len(b) > MaxBytes {
		return nil, ErrTooLong
	}
	v, err := pickVersion(len(b))
	if err != nil {
		return nil, err
	}
	code := interleave(dataCodewords(b, v), versions[v])
	base := newMatrix(17 + 4*v)
	base.function(v)
	base.place(code, v)

	best, bestScore := (*matrix)(nil), 1<<30
	for mask := 0; mask < 8; mask++ {
		c := base.clone()
		c.applyMask(mask)
		c.writeFormat(mask)
		c.writeVersion(v)
		if s := penalty(c); s < bestScore {
			best, bestScore = c, s
		}
	}
	return best.mod, nil
}

// SVG renders the code as a standalone, scalable SVG (4-module quiet zone, no external references).
// The only variable content is the module geometry, so the output is safe to embed in HTML.
func SVG(text string) (string, error) {
	m, err := Encode(text)
	if err != nil {
		return "", err
	}
	const quiet = 4
	size := len(m) + 2*quiet
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 `)
	b.WriteString(itoa(size) + " " + itoa(size))
	b.WriteString(`" shape-rendering="crispEdges" role="img" aria-label="Verification QR code"><rect width="100%" height="100%" fill="#fff"/><path fill="#000" d="`)
	for y, row := range m {
		for x := 0; x < len(row); {
			if !row[x] {
				x++
				continue
			}
			start := x
			for x < len(row) && row[x] {
				x++
			}
			b.WriteString("M" + itoa(start+quiet) + " " + itoa(y+quiet) + "h" + itoa(x-start) + "v1h-" + itoa(x-start) + "z")
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}
