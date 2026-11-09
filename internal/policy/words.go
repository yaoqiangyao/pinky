package policy

// Word tables for commit detection, in the UI languages Pinky supports:
// English, Chinese (Simplified and Traditional), Japanese, Korean, Spanish,
// French, German, Portuguese, Italian and Russian.
//
// Latin and Cyrillic labels are split into lower-case words; a label is a
// commit when its first word is a commit verb or it starts with a commit
// phrase. Labels in scripts without spaces (Chinese, Japanese, Korean) are
// matched by prefix after punctuation, digits and prices are removed.
// A false positive costs one extra approval; a false negative lets a commit
// through, so the tables lean towards matching.

// commitVerbs are first words that make a label a commit.
var commitVerbs = words(
	// English
	"send", "pay", "order", "checkout", "buy", "book", "reserve", "confirm", "submit",
	"delete", "transfer", "post", "purchase", "publish", "tweet", "donate", "subscribe",
	"withdraw", "erase", "repost", "retweet", "renew", "approve", "authorize", "authorise",
	"sell", "swap", "trade", "convert", "repay", "borrow", "lend", "stake", "unstake", "claim", "bridge",
	"redeem", "deposit", "remove", "bin", "trash",
	// Spanish / Portuguese
	"enviar", "pagar", "pedir", "ordenar", "comprar", "reservar", "confirmar", "eliminar",
	"borrar", "transferir", "publicar", "suscribirse", "suscribir", "donar", "excluir", "apagar",
	"assinar", "finalizar", "autorizar", "aprobar", "aprovar", "vender", "intercambiar", "trocar",
	"convertir", "converter", "canjear", "resgatar", "depositar", "retirar", "quitar", "remover",
	// French
	"envoyer", "payer", "commander", "acheter", "réserver", "confirmer", "supprimer",
	"transférer", "publier", "valider", "abonner", "virer", "autoriser", "approuver", "vendre",
	"échanger", "rembourser", "emprunter", "déposer", "retirer", "racheter", "enlever",
	// German
	"senden", "absenden", "bezahlen", "zahlen", "bestellen", "kaufen", "buchen", "reservieren",
	"bestätigen", "löschen", "überweisen", "posten", "veröffentlichen", "abonnieren", "zahlungspflichtig",
	"kostenpflichtig", "spenden", "abheben", "autorisieren", "genehmigen", "freigeben", "verkaufen",
	"tauschen", "umtauschen", "zurückzahlen", "tilgen", "einzahlen", "einlösen", "entfernen",
	// Italian
	"invia", "inviare", "paga", "pagare", "ordina", "acquista", "compra", "prenota", "conferma",
	"elimina", "cancella", "trasferisci", "pubblica", "abbonati", "dona", "preleva", "autorizza", "approva",
	"vendi", "vendere", "scambia", "converti", "rimborsa", "deposita", "riscatta", "rimuovi",
	// Russian
	"отправить", "оплатить", "заказать", "купить", "забронировать", "подтвердить", "удалить",
	"перевести", "опубликовать", "оформить", "подписаться", "пожертвовать", "вывести",
	"продать", "обменять", "конвертировать", "погасить", "занять", "внести", "убрать",
)

// commitPhrases are word sequences that make a label a commit when it starts
// with them.
var commitPhrases = phrases(
	"place order", "place your order", "place my order", "check out", "complete purchase",
	"complete order", "complete payment", "complete booking", "make payment", "make a payment",
	"start subscription", "start free trial", "start trial", "sign out", "log out", "logout",
	"sign off", "remove account", "close account", "cancel subscription", "cancel order",
	"jetzt kaufen", "jetzt bestellen", "jetzt buchen", "jetzt bezahlen", "jetzt zahlen",
	"passer la commande", "passer commande", "faire un don", "se déconnecter",
	"realizar pedido", "tramitar pedido", "hacer pedido", "fazer pedido", "cerrar sesión",
	"finalizar compra", "finalizar pedido", "procedi all'acquisto", "esci dall'account",
	"оформить заказ", "выйти из аккаунта", "move to bin", "move to trash", "sell all", "top up",
	"cash out", "add funds", "send money",
)

// slideVerbs start "slide to pay"-style labels on sliders and buttons.
var slideVerbs = words("slide", "swipe", "tap", "press", "hold", "click")

// cjkCommitPrefixes are prefixes of commit labels in Chinese, Japanese and
// Korean.
var cjkCommitPrefixes = []string{
	// Simplified Chinese
	"发送", "支付", "付款", "下单", "提交", "购买", "立即购买", "立即支付", "立即下单", "结算", "去结算",
	"确认", "删除", "转账", "发布", "发表", "预订", "预约", "订购", "充值", "提现", "捐赠", "订阅", "买单",
	"抢购", "马上抢", "去支付", "去付款", "立即付款", "确认收货", "退出登录", "注销", "登出", "授权", "批准",
	"还款", "立即还款", "分期", "去分期", "立即分期", "借钱", "借款", "买入", "卖出", "赎回", "申购",
	"兑换", "闪兑", "质押", "提币", "充币", "划转", "移除",
	// Traditional Chinese
	"傳送", "發送", "下單", "購買", "立即購買", "結帳", "結賬", "確認", "刪除", "轉帳", "轉賬", "發布",
	"發佈", "發表", "預訂", "預約", "訂購", "儲值", "提領", "訂閱", "登出", "授權",
	"還款", "立即還款", "借錢", "借款", "買入", "賣出", "贖回", "申購", "兌換", "閃兌", "質押", "提幣",
	"充幣", "劃轉",
	// Japanese
	"送信", "支払", "お支払", "注文", "ご注文", "購入", "確定", "削除", "送金", "投稿", "予約", "申し込",
	"申込", "決済", "振込", "振り込", "チェックアウト", "レジに進", "ログアウト", "退会", "寄付",
	"売却", "売る", "両替", "返済", "借入",
	// Korean
	"보내기", "전송", "결제", "주문", "구매", "삭제", "송금", "이체", "게시", "등록", "예약", "제출",
	"구독", "탈퇴", "로그아웃", "기부", "출금", "매도", "매수", "상환", "대출", "환전", "교환", "스왑",
}

// moneyWords make a commit a payment by themselves, with or without an
// amount on the screen: paying, buying and selling, swapping and trading,
// lending and borrowing, moving money in and out. "Convert", "exchange",
// "redeem", "claim" and the like are payments only next to an amount, as
// they also convert files, exchange points and redeem coupons.
var moneyWords = words(
	"pay", "buy", "purchase", "checkout", "sell", "swap", "trade", "repay", "borrow", "lend", "stake",
	"unstake", "bridge", "withdraw", "transfer", "deposit", "donate",
	"pagar", "comprar", "vender", "transferir", "depositar",
	"payer", "acheter", "vendre", "virer", "rembourser", "emprunter",
	"bezahlen", "zahlen", "kaufen", "verkaufen", "überweisen", "abheben", "einzahlen", "zurückzahlen",
	"tilgen", "zahlungspflichtig", "kostenpflichtig",
	"paga", "pagare", "acquista", "compra", "vendi", "vendere", "preleva", "deposita", "rimborsa",
	"оплатить", "купить", "продать", "перевести", "вывести", "погасить",
)

// moneyPhrases make a commit a payment wherever they are in its label.
var moneyPhrases = phrases(
	"place order", "place your order", "place my order", "check out", "complete purchase", "complete order",
	"complete payment", "make payment", "make a payment", "top up", "cash out", "add funds", "send money",
	"passer la commande", "passer commande", "finalizar compra", "оформить заказ",
)

// cjkMoneyKeywords make a CJK commit a payment wherever they are in it:
// "预约还款" (schedule a repayment) as well as "还款".
var cjkMoneyKeywords = []string{
	"支付", "付款", "购买", "买入", "卖出", "买单", "结算", "下单", "充值", "提现", "转账", "还款", "分期", "借钱",
	"借款", "赎回", "申购", "闪兑", "质押", "提币", "充币", "划转", "捐赠",
	"購買", "買入", "賣出", "結帳", "結賬", "儲值", "提領", "轉帳", "轉賬", "還款", "借錢", "贖回", "申購",
	"閃兌", "質押", "提幣", "充幣", "劃轉",
	"支払", "購入", "決済", "送金", "振込", "振り込", "売却", "返済", "借入", "チャージ",
	"결제", "구매", "송금", "이체", "출금", "매도", "매수", "상환", "대출", "충전",
}

// negativeWords mark labels that name a place rather than an action, such as
// "Order history" or "Payment methods".
var negativeWords = words(
	"history", "histories", "details", "detail", "status", "settings", "setting", "list",
	"summary", "tracking", "track", "info", "information", "method", "methods", "options",
	"option", "help", "faq", "policy", "policies", "terms", "receipt", "receipts", "preferences",
	"historial", "detalles", "estado", "ajustes", "métodos", "ayuda", "histórico", "detalhes",
	"historique", "détails", "paramètres", "aide", "verlauf", "einstellungen", "hilfe",
	"cronologia", "dettagli", "impostazioni", "aiuto", "история", "детали", "настройки", "помощь",
	"status", "statut",
)

// cjkNegatives mark CJK labels that name a place rather than an action.
var cjkNegatives = []string{
	"记录", "紀錄", "記錄", "历史", "歷史", "详情", "詳情", "明细", "明細", "设置", "設定", "设定",
	"列表", "方式", "帮助", "幫助", "说明", "說明", "履歴", "詳細", "一覧", "方法", "ヘルプ",
	"내역", "상세", "설정", "목록", "방법", "도움말", "管理", "内容", "內容", "状況", "状态", "狀態",
}

// affirmativeWords confirm a dialog. They are a commit only when the screen
// asks about a commit, as in "Delete this message?" [Cancel] [OK].
var affirmativeWords = words(
	"ok", "okay", "yes", "continue", "proceed", "agree", "accept", "done",
	"sí", "si", "aceptar", "continuar", "oui", "d'accord", "continuer", "ja", "weiter",
	"fortfahren", "sim", "sì", "continua", "да", "ок", "продолжить",
)

var cjkAffirmatives = []string{
	"确定", "好", "好的", "是", "继续", "同意", "確定", "繼續", "はい", "続ける", "続行", "同意する",
	"확인", "예", "네", "계속",
}

// installVerbs are first words of install buttons in app stores and the
// system's package dialog.
var installVerbs = words(
	"install", "get", "uninstall", "update", "reinstall", "download",
	"instalar", "obtener", "desinstalar", "actualizar", "descargar", "obter", "atualizar", "baixar",
	"installer", "obtenir", "désinstaller", "télécharger", "installieren", "laden", "deinstallieren",
	"aktualisieren", "herunterladen", "installa", "ottieni", "disinstalla", "aggiorna", "scarica",
	"установить", "получить", "обновить", "скачать", "загрузить",
)

var cjkInstallPrefixes = []string{
	"安装", "获取", "卸载", "下载", "更新", "安裝", "取得", "解除安裝", "下載",
	"インストール", "入手", "アンインストール", "アップデート", "ダウンロード",
	"설치", "받기", "제거", "업데이트", "다운로드",
}

// callWords start call buttons.
var callWords = words(
	"call", "dial", "redial", "llamar", "appeler", "anrufen", "ligar", "chiama", "chiamare", "позвонить",
)

var callPhrases = phrases("voice call", "video call", "call back", "facetime audio", "facetime video")

var cjkCallPrefixes = []string{
	"呼叫", "拨打", "撥打", "打电话", "打電話", "通话", "語音通話", "语音通话", "视频通话", "視訊通話",
	"電話をかけ", "発信", "通話", "전화 걸기", "전화걸기", "통화", "발신",
}

// settingVerbs start buttons that change a setting, in the Settings app.
var settingVerbs = words(
	"remove", "forget", "reset", "erase", "disable", "enable", "unpair", "disconnect", "revoke",
	"turn", "clear", "restore", "factory",
)

// searchWords mark search fields, which read-only mode may still type into.
var searchWords = []string{
	"search", "find", "query", "lookup", "搜索", "搜尋", "查找", "搜一搜", "检索", "検索", "검색",
	"buscar", "búsqueda", "busca", "rechercher", "recherche", "suchen", "suche", "pesquisar",
	"pesquisa", "cerca", "cercare", "ricerca", "поиск", "найти", "искать",
}

// passwordWords mark password fields.
var passwordWords = []string{
	"password", "passwort", "kennwort", "contraseña", "mot de passe", "senha", "parola d'ordine",
	"пароль", "密码", "密碼", "パスワード", "비밀번호", "암호", "passcode", "passphrase",
}

// otpWords mark one-time-code fields. Plain "code" and "PIN" count too,
// unless otpNegatives say it is a promo, postal or invite code.
var otpWords = []string{
	"otp", "one-time", "one time", "verification", "verify", "security code", "sms code", "auth code",
	"authentication", "2fa", "two-factor", "access code", "login code", "confirmation code", "code",
	"验证码", "校验码", "动态码", "动态密码", "短信码", "驗證碼", "認證碼", "确认码", "確認碼",
	"確認コード", "認証コード", "認証番号", "ワンタイム", "セキュリティコード", "コード",
	"인증번호", "인증 번호", "인증코드", "보안코드", "보안 코드",
	"código", "codigo", "verificación", "verificação", "bestätigungscode", "sicherheitscode",
	"verifizierungscode", "codice", "код",
}

var otpTokens = words("pin", "tan", "mtan", "otp")

var otpNegatives = []string{
	"promo", "coupon", "discount", "gift", "referral", "invite", "invitation", "zip", "postal",
	"postcode", "post code", "country", "area code", "voucher", "redeem", "qr", "source",
	"优惠", "邀请", "邀請", "邮编", "郵編", "邮政", "兑换", "兌換", "礼品", "禮品", "推荐",
	"クーポン", "招待", "郵便", "쿠폰", "초대", "우편", "promocional", "cupón", "cupom", "gutschein",
	"postleitzahl", "cep", "sconto", "invito", "промокод", "купон", "индекс",
}

// codeWords and codePhrases mark notifications that carry a one-time code:
// words are matched whole, so "shopping" does not count as "pin".
var codeWords = words(
	"code", "codes", "otp", "passcode", "verification", "verify", "2fa", "pin", "login", "security", "token",
	"código", "codigo", "codice", "code", "bestätigungscode", "sicherheitscode", "код", "пароль", "tan",
)

var codePhrases = []string{"one-time", "one time", "sign-in", "sign in"}

// codeNouns are the codeWords that name a code outright.
var codeNouns = words(
	"code", "codes", "otp", "passcode", "2fa", "pin", "token", "código", "codigo", "codice", "bestätigungscode",
	"sicherheitscode", "код", "пароль", "tan",
)

// cjkCodeKeywords are matched anywhere in CJK text.
var cjkCodeKeywords = []string{
	"验证码", "校验码", "动态码", "驗證碼", "認證碼", "確認碼", "确认码", "认证码", "コード", "認証", "인증",
}

// notCodePhrases name codes that are not one-time codes; their code words
// do not count (hasCodeWords). Lower case.
var notCodePhrases = []string{
	"zip code", "zipcode", "postal code", "post code", "postcode", "area code", "country code", "promo code",
	"promotional code", "coupon code", "discount code", "gift code", "gift card code", "referral code",
	"invite code", "invitation code", "voucher code", "redeem code", "redemption code", "source code", "qr code",
	"bar code", "barcode", "error code", "status code", "color code", "colour code", "dress code",
	"código postal", "código promocional", "código de descuento", "codice postale", "codice sconto",
	"codice promozionale", "gutscheincode", "rabattcode", "クーポンコード", "招待コード", "プロモーションコード",
	"ギフトコード", "qrコード", "バーコード", "エラーコード",
}

// currencySigns before a number, and currencyWords before or after it,
// make it an amount, not a code.
const currencySigns = "$€£¥₩₹₽₫฿₺₴₱￥＄"

var currencyWords = words(
	"usd", "rmb", "cny", "eur", "gbp", "jpy", "hkd", "twd", "krw", "inr", "rs", "sgd", "aud", "cad",
	"chf", "myr", "thb", "rub", "brl", "mxn", "idr", "php", "vnd", "yuan", "dollar", "dollars", "euro",
	"euros", "pound", "pounds", "yen", "won", "rupee", "rupees",
)

// unitRunes and unitWords after a number make it an amount, a date or a
// measure, not a code.
const unitRunes = "元块圆円원年月日号%‰€₽₫°"

var unitWords = words(
	"km", "kg", "mb", "gb", "kb", "tb", "mah", "ml", "mm", "cm", "kcal", "cal", "mi", "ft", "points", "pts", "steps",
)

// monthWords before a year make it a date.
var monthWords = words(
	"january", "february", "march", "april", "may", "june", "july", "august", "september", "october",
	"november", "december", "jan", "feb", "mar", "apr", "jun", "jul", "aug", "sep", "sept", "oct", "nov", "dec",
)

func words(ws ...string) map[string]bool {
	m := make(map[string]bool, len(ws))
	for _, w := range ws {
		m[w] = true
	}
	return m
}

func phrases(ps ...string) [][]string {
	out := make([][]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, tokenize(p))
	}
	return out
}
