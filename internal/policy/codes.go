package policy

import (
	"sort"
	"strings"
	"unicode"

	"github.com/DIYgod/pinky/internal/screen"
)

// One-time codes. While "Hide codes from agents" is on (the rules file's
// mask_codes, on by default), the digits of one-time codes are masked in
// events (FilterEvents) and in the labels of the elements on the screen,
// OCR lines included (MaskScreen), before an agent reads them, and the
// gateway covers the masked elements in pictures of the screen.
//
// A text carries a code when it talks about one (codeWords, codePhrases,
// cjkCodeKeywords; "promo code", "zip code" and the like do not count) and
// holds a number shaped like one: four to eight digits, or two groups of
// three or four ("482 913", "4829-1357"). Every such number in the text is
// masked. A number that is part of something else never is: a phone
// number or a date (three groups or more, a "+" or "(" before it), an
// amount (a currency before it, a decimal part, a unit such as 元 or %
// after it), a year after a month or a day ("November 1, 2026"), or digits
// inside a word ("A1234").
//
// On the screen, words and code may sit in different elements. Lines that
// OCR read from one wrapped text count as one text, and a number shown on
// its own ("482913") counts as a code when an element right next to it
// talks about one ("Your verification code").

// MaskCodes masks the one-time codes in a text that talks about a code, in
// any supported language: "Your code is 482913" becomes "Your code is
// ••••••".
func MaskCodes(text string) string {
	if !hasCodeWords(text) {
		return text
	}
	out, _ := maskNumbers(text)
	return out
}

// MaskScreen masks the one-time codes in the labels of a screen's elements
// while "Hide codes from agents" is on, and marks those elements Masked. It
// reports how many elements it masked. The screen gets a new element slice
// when any is masked.
func (e *Engine) MaskScreen(s *screen.Screen) int {
	if s == nil || !e.HideCodes() {
		return 0
	}
	els, n := MaskElements(s.Elements)
	s.Elements = els
	return n
}

// MaskElements masks the one-time codes in the labels of elements on one
// screen, whatever the setting, and marks the elements it changed Masked.
// It returns els itself when nothing changed, else a copy, and how many
// elements it masked.
func MaskElements(els []screen.Element) ([]screen.Element, int) {
	var out []screen.Element
	n := 0
	set := func(i int, label string) {
		if out == nil {
			out = append([]screen.Element(nil), els...)
		}
		out[i].Label, out[i].Masked = label, true
		n++
	}
	for _, para := range paragraphs(els) {
		texts := make([]string, len(para))
		for k, i := range para {
			texts[k] = els[i].Label
		}
		if !hasCodeWords(strings.Join(texts, "\n")) && !hasCodeWords(strings.Join(texts, "")) {
			continue
		}
		for _, i := range para {
			if m, ok := maskNumbers(els[i].Label); ok {
				set(i, m)
			}
		}
	}
	for i, e := range els {
		if e.Label == "" || (out != nil && out[i].Masked) {
			continue
		}
		if hasCodeWords(e.Label) {
			if m, ok := maskNumbers(e.Label); ok {
				set(i, m)
			}
			continue
		}
		if m, ok := maskLoneCode(els, i); ok {
			set(i, m)
		}
	}
	if out == nil {
		return els, 0
	}
	return out, n
}

// maskLoneCode masks element i when its label is a number shaped like a
// code, nearly on its own ("482913", "G-482913"), and an element right
// above, below or before it talks about a code without showing a number
// of its own ("Your verification code", "验证码"). A year ("2026") does not
// count here.
func maskLoneCode(els []screen.Element, i int) (string, bool) {
	c := els[i]
	if c.Bounds.Empty() || hasNegative(c.Label) {
		return "", false
	}
	rs := []rune(c.Label)
	spans := codeNumbers(rs)
	if !lone(rs, spans) || (len(spans) == 1 && yearLike(rs, spans[0])) {
		return "", false
	}
	led := false
	for j, l := range els {
		if j == i || l.Label == "" || l.Bounds.Empty() || !nextTo(l.Bounds, c.Bounds) {
			continue
		}
		if hasNegative(l.Label) {
			return "", false // "Zip code" next to it
		}
		if namesCode(l.Label) && len(codeNumbers([]rune(l.Label))) == 0 {
			led = true
		}
	}
	if !led {
		return "", false
	}
	return maskNumbers(c.Label)
}

// namesCode reports whether a text names a code outright ("Verification
// code", "PIN", "验证码"), not only a sign-in or security matter around
// one.
func namesCode(text string) bool {
	lower := strings.ToLower(text)
	for _, p := range notCodePhrases {
		lower = strings.ReplaceAll(lower, p, " ")
	}
	return anyWord(tokenize(lower), codeNouns) || containsAny(lower, []string{"one-time", "one time"}) ||
		containsAny(lower, cjkCodeKeywords)
}

// lone reports whether a label is little more than the code numbers in
// it: at most two other letters or digits.
func lone(rs []rune, spans []span) bool {
	if len(spans) == 0 {
		return false
	}
	other := 0
	k := 0
	for i, r := range rs {
		for k < len(spans) && i >= spans[k].end {
			k++
		}
		if k < len(spans) && i >= spans[k].start {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			other++
		}
	}
	return other <= 2
}

// nextTo reports whether element l, a line or two of text, sits right next
// to element c: above or below it, overlapping it sideways, or before it
// on the same row. An element that encloses the other, or a tall one such
// as a list, is not next to it.
func nextTo(l, c screen.Rect) bool {
	if l.H > 4*c.H {
		return false
	}
	h, slack := max(l.H, c.H), min(l.H, c.H)/4
	if overlap(l.X, l.W, c.X, c.W) > 0 {
		if gap := c.Y - (l.Y + l.H); gap >= -slack && gap <= 2*h {
			return true
		}
		if gap := l.Y - (c.Y + c.H); gap >= -slack && gap <= 2*h {
			return true
		}
	}
	if overlap(l.Y, l.H, c.Y, c.H)*2 >= min(l.H, c.H) {
		if gap := c.X - (l.X + l.W); gap >= -slack && gap <= 6*h {
			return true
		}
	}
	return false
}

// paragraphs groups OCR lines that wrap one text: lines of about the same
// height stacked closely, overlapping sideways. It returns the groups of
// two lines or more, each from the top down, as element indexes.
func paragraphs(els []screen.Element) [][]int {
	var lines []int
	for i, e := range els {
		if e.Source == screen.SourceOCR && e.Label != "" && !e.Bounds.Empty() && !strings.Contains(e.Label, "\n") {
			lines = append(lines, i)
		}
	}
	if len(lines) < 2 {
		return nil
	}
	parent := map[int]int{}
	var find func(int) int
	find = func(i int) int {
		p, ok := parent[i]
		if !ok || p == i {
			return i
		}
		r := find(p)
		parent[i] = r
		return r
	}
	for _, a := range lines {
		for _, b := range lines {
			if a != b && wrapsTo(els[a].Bounds, els[b].Bounds) {
				parent[find(b)] = find(a)
			}
		}
	}
	groups := map[int][]int{}
	for _, i := range lines {
		r := find(i)
		groups[r] = append(groups[r], i)
	}
	var out [][]int
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		sort.Slice(g, func(x, y int) bool { return els[g[x]].Bounds.Y < els[g[y]].Bounds.Y })
		out = append(out, g)
	}
	sort.Slice(out, func(x, y int) bool { return out[x][0] < out[y][0] })
	return out
}

// wrapsTo reports whether OCR line b continues line a: right below it,
// about as tall, overlapping it sideways.
func wrapsTo(a, b screen.Rect) bool {
	h := max(a.H, b.H)
	if min(a.H, b.H)*8 < h*5 {
		return false
	}
	gap := b.Y - (a.Y + a.H)
	return gap >= -h/4 && gap*5 <= h*3 && overlap(a.X, a.W, b.X, b.W) > 0
}

// overlap is the length two intervals share.
func overlap(a, al, b, bl int) int {
	return max(0, min(a+al, b+bl)-max(a, b))
}

// hasCodeWords reports whether a text talks about a code, in any supported
// language; "promo code", "zip code" and the like do not count.
func hasCodeWords(text string) bool {
	lower := strings.ToLower(text)
	for _, p := range notCodePhrases {
		if strings.Contains(lower, p) {
			lower = strings.ReplaceAll(lower, p, " ")
		}
	}
	return anyWord(tokenize(lower), codeWords) || containsAny(lower, codePhrases) || containsAny(lower, cjkCodeKeywords)
}

// hasNegative reports whether a text names a code that is not a one-time
// code, such as a promo, postal or invite code.
func hasNegative(text string) bool {
	return containsAny(strings.ToLower(text), otpNegatives)
}

// span is a range of runes, [start, end).
type span struct{ start, end int }

// maskNumbers masks the digits of every number shaped like a code in text.
func maskNumbers(text string) (string, bool) {
	rs := []rune(text)
	spans := codeNumbers(rs)
	if len(spans) == 0 {
		return text, false
	}
	for _, s := range spans {
		for i := s.start; i < s.end; i++ {
			if isDigit(rs[i]) {
				rs[i] = '•'
			}
		}
	}
	return string(rs), true
}

// codeNumbers finds the numbers shaped like a one-time code in rs.
func codeNumbers(rs []rune) []span {
	var out []span
	for i := 0; i < len(rs); {
		if !isDigit(rs[i]) {
			i++
			continue
		}
		// A run of digit groups joined by single spaces or hyphens.
		var groups []span
		spaced := true
		j := i
		for {
			k := j
			for k < len(rs) && isDigit(rs[k]) {
				k++
			}
			groups = append(groups, span{j, k})
			if k+1 < len(rs) && isJoiner(rs[k]) && isDigit(rs[k+1]) {
				spaced = spaced && unicode.IsSpace(rs[k])
				j = k + 1
				continue
			}
			j = k
			break
		}
		i = j
		whole := span{groups[0].start, groups[len(groups)-1].end}
		switch {
		case len(groups) == 1:
			if n := whole.end - whole.start; n >= 4 && n <= 8 && clearBefore(rs, whole) && clearAfter(rs, whole) {
				out = append(out, whole)
			}
		case len(groups) == 2 && pairShaped(groups) && clearBefore(rs, whole) && clearAfter(rs, whole):
			out = append(out, whole)
		case len(groups) == 2 && spaced && clearBefore(rs, whole):
			// Two numbers side by side ("482913 5分钟"): each on its own.
			for k, g := range groups {
				n := g.end - g.start
				if n < 4 || n > 8 || (k == 1 && !clearAfter(rs, g)) {
					continue
				}
				out = append(out, g)
			}
		}
		// Three groups or more: a phone, card or date number.
	}
	return out
}

// pairShaped reports whether two groups make a code: 3+3 or 4+4.
func pairShaped(g []span) bool {
	a, b := g[0].end-g[0].start, g[1].end-g[1].start
	return a == b && (a == 3 || a == 4)
}

// clearBefore reports whether what comes before the number at s leaves it
// a code: not a letter of a word, a phone number's "+" or "(", a currency,
// the whole part of a decimal, or a month or day before a year.
func clearBefore(rs []rune, s span) bool {
	i := s.start
	if i == 0 {
		return true
	}
	b := rs[i-1]
	switch {
	case isWordLetter(b), strings.ContainsRune("+(＋（)）", b), strings.ContainsRune(currencySigns, b):
		return false
	case strings.ContainsRune(".,:/", b) && i >= 2 && isDigit(rs[i-2]):
		return false // 12.3456, 1,2345, 10:2345, 11/2026
	case unicode.IsSpace(b):
		if i >= 2 && strings.ContainsRune(currencySigns+")）", rs[i-2]) {
			return false // ¥ 1234, (415) 555 1234
		}
		w := wordBefore(rs, i-1)
		if currencyWords[w] {
			return false // USD 1234
		}
		if yearLike(rs, s) && (monthWords[w] || (i >= 3 && rs[i-2] == ',' && isDigit(rs[i-3]))) {
			return false // Nov 2026, November 1, 2026
		}
	}
	return true
}

// clearAfter reports whether what comes after the number at s leaves it a
// code: not a letter of a word, a decimal part, a time, or a unit.
func clearAfter(rs []rune, s span) bool {
	j := s.end
	if j >= len(rs) {
		return true
	}
	a := rs[j]
	switch {
	case isWordLetter(a), strings.ContainsRune(unitRunes, a):
		return false // 1234px, 1234元, 2026年
	case strings.ContainsRune(".,:/", a) && j+1 < len(rs) && isDigit(rs[j+1]):
		return false // 1280.00, 1,234, 2026/11/08
	case unicode.IsSpace(a) && j+1 < len(rs):
		if strings.ContainsRune(unitRunes, rs[j+1]) {
			return false // 1234 元
		}
		w := wordAfter(rs, j+1)
		return !currencyWords[w] && !unitWords[w]
	}
	return true
}

// yearLike reports whether the number at s reads as a year, 1900 to 2099.
func yearLike(rs []rune, s span) bool {
	if s.end-s.start != 4 {
		return false
	}
	p := string(rs[s.start : s.start+2])
	return p == "19" || p == "20"
}

// wordBefore returns the lower-case word of letters that ends at rs[i-1],
// over one full stop ("Rs. 1234").
func wordBefore(rs []rune, i int) string {
	if i > 0 && rs[i-1] == '.' {
		i--
	}
	j := i
	for j > 0 && unicode.IsLetter(rs[j-1]) {
		j--
	}
	return strings.ToLower(string(rs[j:i]))
}

// wordAfter returns the lower-case word of letters that starts at rs[i].
func wordAfter(rs []rune, i int) string {
	j := i
	for j < len(rs) && unicode.IsLetter(rs[j]) {
		j++
	}
	return strings.ToLower(string(rs[i:j]))
}

func isDigit(r rune) bool { return (r >= '0' && r <= '9') || (r >= '０' && r <= '９') }

func isJoiner(r rune) bool { return r == ' ' || r == ' ' || r == '-' || r == '‐' || r == '‑' }

// isWordLetter reports whether r is a letter of a script that writes words
// with spaces between them, so that digits next to it are part of the word.
func isWordLetter(r rune) bool {
	return unicode.IsLetter(r) && unicode.In(r, unicode.Latin, unicode.Greek, unicode.Cyrillic)
}
