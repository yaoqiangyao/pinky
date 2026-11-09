package policy

import (
	"strings"
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
)

func TestMaskCodesMasks(t *testing.T) {
	cases := map[string]string{
		"【京东】验证码：482913，您正在登录京东账号，如非本人操作请忽略。": "【京东】验证码：••••••，您正在登录京东账号，如非本人操作请忽略。",
		"您的验证码为482913，5分钟内有效":                 "您的验证码为••••••，5分钟内有效",
		"【银行】您尾号6688的卡正在支付128.00元，验证码482913":  "【银行】您尾号••••的卡正在支付128.00元，验证码••••••",
		"【某银行】您正在支付1280.00元，动态码 482913":       "【某银行】您正在支付1280.00元，动态码 ••••••",
		"驗證碼 4829": "驗證碼 ••••",
		"482913 is your Instagram code. Don't share it.":          "•••••• is your Instagram code. Don't share it.",
		"Use 4829-1357 as your Microsoft account security code":   "Use ••••-•••• as your Microsoft account security code",
		"Your WhatsApp code: 482-913. Don't share this code":      "Your WhatsApp code: •••-•••. Don't share this code",
		"Your Example verification code is 482913.":               "Your Example verification code is ••••••.",
		"Sign in to Example: 48291":                               "Sign in to Example: •••••",
		"認証コード：482913":                                            "認証コード：••••••",
		"인증번호 [482913]를 입력해 주세요":                                  "인증번호 [••••••]를 입력해 주세요",
		"Ihr Bestätigungscode lautet 482913":                      "Ihr Bestätigungscode lautet ••••••",
		"Tu código es 482913":                                     "Tu código es ••••••",
		"Ваш код: 482913":                                         "Ваш код: ••••••",
		"Code 482913 5分钟内有效":                                      "Code •••••• 5分钟内有效",
		"Your one-time passcode is ４８２９１３":                        "Your one-time passcode is ••••••",
		"Your promo code is SAVE20 and your login code is 482913": "Your promo code is SAVE20 and your login code is ••••••",
	}
	for in, want := range cases {
		if got := MaskCodes(in); got != want {
			t.Errorf("MaskCodes(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

// Numbers that are not one-time codes stay as they are, also next to code
// words.
func TestMaskCodesKeepsOtherNumbers(t *testing.T) {
	for _, in := range []string{
		// No words about a code.
		"Your order 482913 has shipped",
		"订单 482913 已发货",
		"Call 4008 123 456 or 13812345678",
		"会议室 2046，下午 3 点",
		// Code words, but the number is something else.
		"Your code costs $1234",
		"验证码已发送，支付金额 ¥ 1280",
		"Security update: 2026-11-01",
		"安全补丁：2026年11月1日",
		"Last login November 1, 2026 at 10:23",
		"Login from 192.168.1.42",
		"Questions about your code? Call +86 138 1234 5678",
		"Questions about your code? Call (415) 555 1234",
		"Verification of 1280.00 USD",
		"Verification fee 1280 USD",
		"Your code expires in 15 minutes",
		"Security: iPhone15 Pro, build 25A1234",
		"Storage security: 1024 GB",
		"验证码将发送至 138 1234 5678",
		// Codes that are not one-time codes.
		"Zip code 10001",
		"Postal code: 100080",
		"Promo code 2024 saves 20%",
		"Error code 4013",
		"Area code 0755",
	} {
		if got := MaskCodes(in); got != in {
			t.Errorf("MaskCodes(%q) = %q, want it unchanged", in, got)
		}
	}
}

func ocrLine(label string, x, y, w, h int) screen.Element {
	return screen.Element{Role: screen.RoleText, Label: label, Source: screen.SourceOCR, Bounds: screen.Rect{X: x, Y: y, W: w, H: h}}
}

func treeText(label string, x, y, w, h int) screen.Element {
	return screen.Element{Role: screen.RoleText, Label: label, Source: screen.SourceTree, Bounds: screen.Rect{X: x, Y: y, W: w, H: h}}
}

func labels(els []screen.Element) string {
	var b strings.Builder
	for _, e := range els {
		b.WriteString(e.Label)
		if e.Masked {
			b.WriteString(" [masked]")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestMaskElements(t *testing.T) {
	cases := []struct {
		name string
		in   []screen.Element
		want string
	}{{
		name: "a message row",
		in: []screen.Element{
			treeText("Messages", 40, 100, 400, 60),
			{Role: screen.RoleButton, Label: "106575, 【京东】验证码：482913，您正在登录, 10:23", Bounds: screen.Rect{X: 0, Y: 200, W: 1080, H: 180}},
			{Role: screen.RoleButton, Label: "Mom, Dinner at 7? Room 2046, 09:12", Bounds: screen.Rect{X: 0, Y: 380, W: 1080, H: 180}},
		},
		// The sender's short number is masked too: every number shaped like
		// a code in a code message is.
		want: "Messages\n••••••, 【京东】验证码：••••••，您正在登录, 10:23 [masked]\nMom, Dinner at 7? Room 2046, 09:12\n",
	}, {
		name: "an iPhone banner read by OCR, wrapped",
		in: []screen.Element{
			ocrLine("MESSAGES", 80, 120, 200, 30),
			ocrLine("now", 900, 120, 80, 30),
			ocrLine("【某银行】您本次操作的验", 80, 170, 800, 44),
			ocrLine("证码为482913，请勿泄露。", 80, 222, 780, 44),
		},
		want: "MESSAGES\nnow\n【某银行】您本次操作的验\n证码为••••••，请勿泄露。 [masked]\n",
	}, {
		name: "English wrapped by OCR",
		in: []screen.Element{
			ocrLine("Your Example verification code", 80, 600, 900, 50),
			ocrLine("is 482913. It expires in 10", 80, 660, 820, 50),
			ocrLine("minutes.", 80, 720, 240, 50),
			ocrLine("Order 778899", 80, 1200, 400, 50),
		},
		want: "Your Example verification code\nis ••••••. It expires in 10 [masked]\nminutes.\nOrder 778899\n",
	}, {
		name: "a code shown on its own under its label",
		in: []screen.Element{
			treeText("Use this code to sign in:", 80, 400, 900, 48),
			treeText("482 913", 80, 470, 400, 96),
			treeText("This code expires in 10 minutes.", 80, 600, 900, 40),
		},
		want: "Use this code to sign in:\n••• ••• [masked]\nThis code expires in 10 minutes.\n",
	}, {
		name: "a code typed into its field, after its label",
		in: []screen.Element{
			treeText("验证码", 40, 800, 200, 60),
			{Role: screen.RoleInput, Label: "482913", Bounds: screen.Rect{X: 300, Y: 790, W: 500, H: 80}, States: []string{screen.StateEditable}},
		},
		want: "验证码\n•••••• [masked]\n",
	}, {
		name: "numbers far from code words, or not codes",
		in: []screen.Element{
			treeText("Verification code", 40, 200, 600, 60),
			treeText("2046", 40, 1600, 200, 60), // far below
			treeText("Zip code", 40, 800, 300, 60),
			treeText("10001", 40, 880, 300, 60),
			treeText("Sign in", 40, 1000, 300, 60),
			treeText("© 2026", 40, 1080, 300, 40),
			treeText("Your order", 40, 1200, 300, 60),
			treeText("482913", 40, 1270, 300, 60),
		},
		want: "Verification code\n2046\nZip code\n10001\nSign in\n© 2026\nYour order\n482913\n",
	}, {
		name: "a container that holds the code word",
		in: []screen.Element{
			{Role: screen.RoleOther, Label: "Security", Bounds: screen.Rect{X: 0, Y: 0, W: 1080, H: 2000}},
			treeText("Steps today", 40, 300, 400, 60),
			treeText("12345", 40, 380, 400, 80),
		},
		want: "Security\nSteps today\n12345\n",
	}, {
		name: "a price list in a shop",
		in: []screen.Element{
			treeText("Promo code", 40, 300, 400, 60),
			treeText("¥1299", 40, 380, 400, 60),
			treeText("1299", 500, 380, 200, 60),
		},
		want: "Promo code\n¥1299\n1299\n",
	}}
	for _, c := range cases {
		in := append([]screen.Element(nil), c.in...)
		got, n := MaskElements(in)
		if labels(got) != c.want {
			t.Errorf("%s:\n got:\n%s\nwant:\n%s", c.name, labels(got), c.want)
		}
		if want := strings.Count(c.want, "[masked]"); n != want {
			t.Errorf("%s: masked %d, want %d", c.name, n, want)
		}
		if labels(in) != labels(c.in) {
			t.Errorf("%s: changed its input", c.name)
		}
	}
}

func TestMaskScreenFollowsTheSetting(t *testing.T) {
	e := Default()
	if !e.HideCodes() {
		t.Fatal("codes are not hidden by default")
	}
	s := &screen.Screen{Elements: []screen.Element{treeText("Your code is 482913", 0, 0, 100, 40)}}
	if n := e.MaskScreen(s); n != 1 || s.Elements[0].Label != "Your code is ••••••" || !s.Elements[0].Masked {
		t.Fatalf("masked %d: %+v", n, s.Elements)
	}
	off := false
	e = New(File{MaskCodes: &off}, nil)
	s = &screen.Screen{Elements: []screen.Element{treeText("Your code is 482913", 0, 0, 100, 40)}}
	if n := e.MaskScreen(s); n != 0 || s.Elements[0].Label != "Your code is 482913" {
		t.Fatalf("masked with the setting off: %+v", s.Elements)
	}
}

func TestFilterEventsMasksToasts(t *testing.T) {
	got := Default().FilterEvents([]screen.Event{{Kind: screen.EventToast, Text: "Code 482913 copied"}})
	if got[0].Text != "Code •••••• copied" {
		t.Fatalf("toast: %q", got[0].Text)
	}
}
