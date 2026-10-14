package mail

import (
	"fmt"
	"html"
	"strings"
)

// Brand names per locale.
const (
	BrandEN = "Hosbah"
	BrandAR = "حوسبة"
)

// CodeEmail is the 6-digit code for verifying an address (purpose "verify"), resetting a
// password ("reset") or signing in without one ("login"), in English or Arabic. A code rather
// than a link: it works the same in any client and cannot be pre-fetched by a link scanner.
func CodeEmail(locale, purpose, code string) (subject, text, htmlBody string) {
	if locale != "ar" {
		locale = "en"
	}
	spaced := strings.Join(strings.Split(code, ""), " ")
	box := fmt.Sprintf(`<p style="margin:16px 0;font-size:28px;letter-spacing:6px;font-weight:600;font-family:ui-monospace,Menlo,monospace;" dir="ltr">%s</p>`, html.EscapeString(code))

	type copyText struct{ subject, lead, heading, intro, foot string }
	var c copyText
	switch {
	case locale == "ar" && purpose == "login":
		c = copyText{"رمز تسجيل الدخول إلى " + BrandAR, "رمز تسجيل الدخول إلى حسابك على " + BrandAR + ":", "تسجيل الدخول",
			"أدخل هذا الرمز لتسجيل الدخول:",
			"الرمز صالح لمدة 15 دقيقة. إذا لم تطلب ذلك، تجاهل الرسالة — لا يمكن لأحد الدخول بدون هذا الرمز."}
	case locale == "ar" && purpose == "reset":
		c = copyText{"رمز إعادة تعيين كلمة المرور على " + BrandAR, "رمز إعادة تعيين كلمة المرور لحسابك على " + BrandAR + ":", "إعادة تعيين كلمة المرور",
			"أدخل هذا الرمز لاختيار كلمة مرور جديدة:",
			"الرمز صالح لمدة 15 دقيقة. إذا لم تطلب ذلك، تجاهل الرسالة — كلمة مرورك لم تتغير."}
	case locale == "ar" && purpose == "delete":
		c = copyText{"رمز حذف حسابك على " + BrandAR, "رمز تأكيد حذف حسابك على " + BrandAR + ":", "حذف الحساب",
			"أدخل هذا الرمز لتأكيد حذف حسابك نهائيًا:",
			"الرمز صالح لمدة 15 دقيقة. إذا لم تطلب حذف حسابك، تجاهل الرسالة وغيّر كلمة مرورك — لن يُحذف شيء بدون هذا الرمز."}
	case locale == "ar":
		c = copyText{"رمز تأكيد بريدك على " + BrandAR, "رمز تأكيد بريدك الإلكتروني لحسابك الجديد على " + BrandAR + ":", "أكّد بريدك الإلكتروني",
			"أدخل هذا الرمز لتفعيل حسابك:",
			"الرمز صالح لمدة 15 دقيقة. إذا لم تنشئ حسابًا، تجاهل الرسالة."}
	case purpose == "login":
		c = copyText{"Your " + BrandEN + " sign-in code", "Your code to sign in to " + BrandEN + ":", "Sign in",
			"Enter this code to sign in:",
			"It works for 15 minutes. If you did not ask for this, ignore this email — nobody can sign in without the code."}
	case purpose == "reset":
		c = copyText{"Your " + BrandEN + " password reset code", "Your code to reset your " + BrandEN + " password:", "Reset your password",
			"Enter this code to choose a new password:",
			"It works for 15 minutes. If you did not ask for this, ignore this email — your password has not changed."}
	case purpose == "delete":
		c = copyText{"Your " + BrandEN + " account deletion code", "Your code to delete your " + BrandEN + " account:", "Delete your account",
			"Enter this code to confirm deleting your account for good:",
			"It works for 15 minutes. If you did not ask to delete your account, ignore this email and change your password — nothing is deleted without the code."}
	default:
		c = copyText{"Your " + BrandEN + " verification code", "Your code to verify the email for your new " + BrandEN + " account:", "Verify your email",
			"Enter this code to activate your account:",
			"It works for 15 minutes. If you did not create an account, ignore this email."}
	}

	brand := BrandEN
	if locale == "ar" {
		brand = BrandAR
	}
	subject = c.subject
	text = c.lead + "\n\n" + spaced + "\n\n" + c.foot + "\n\n— " + brand
	htmlBody = shell(locale, c.heading,
		`<p style="margin:0;">`+html.EscapeString(c.intro)+`</p>`+box+`<p style="margin:0;">`+html.EscapeString(c.foot)+`</p>`,
		html.EscapeString(brand))
	return subject, text, htmlBody
}

// Palette: literal hex, because custom properties resolve to nothing in a mail client.
const (
	colourInk   = "#0F172A"
	colourPaper = "#F8FAFC"
	colourMuted = "#475569"
	colourLine  = "#E2E8F0"
)

// shell is a table-based frame with inlined styles (what mail clients support), rendered RTL for
// Arabic rather than an LTR template with Arabic poured into it.
func shell(locale, heading, bodyHTML, footerHTML string) string {
	dir, align := "ltr", "left"
	font := "'Segoe UI', Helvetica, Arial, sans-serif"
	if locale == "ar" {
		dir, align = "rtl", "right"
		font = "'IBM Plex Sans Arabic', 'Segoe UI', Tahoma, sans-serif"
	}
	return fmt.Sprintf(`<!doctype html>
<html dir="%s" lang="%s">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width"></head>
<body style="margin:0;padding:0;background:%s;">
  <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" dir="%s" style="background:%s;padding:32px 16px;">
    <tr><td align="center">
      <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;background:#ffffff;border:1px solid %s;">
        <tr><td style="padding:28px 28px 8px 28px;text-align:%s;">
          <h1 style="margin:0;font-family:%s;font-size:20px;font-weight:500;color:%s;">%s</h1>
        </td></tr>
        <tr><td style="padding:8px 28px 24px 28px;text-align:%s;font-family:%s;font-size:15px;line-height:1.6;color:%s;">
          %s
        </td></tr>
        <tr><td style="padding:16px 28px;border-top:1px solid %s;text-align:%s;font-family:ui-monospace,Menlo,monospace;font-size:12px;color:%s;">
          %s
        </td></tr>
      </table>
    </td></tr>
  </table>
</body></html>`,
		dir, locale, colourPaper, dir, colourPaper, colourLine,
		align, font, colourInk, html.EscapeString(heading),
		align, font, colourMuted, bodyHTML,
		colourLine, align, colourMuted, footerHTML)
}
