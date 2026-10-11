import { NextResponse, type NextRequest } from "next/server"

import { DEFAULT_LOCALE, isLocale, LOCALE_COOKIE, type Locale } from "@/lib/i18n-config"

/*
  Locale routing only. The API is the security boundary: client pages call
  /api/me and send the visitor to /login on a 401, so nothing here reads the
  session cookie.
*/

function preferred(request: NextRequest): Locale {
  const cookie = request.cookies.get(LOCALE_COOKIE)?.value
  if (isLocale(cookie)) return cookie
  const accept = request.headers.get("accept-language") ?? ""
  for (const part of accept.split(",")) {
    const tag = part.split(";")[0]?.trim().toLowerCase().slice(0, 2)
    if (isLocale(tag)) return tag
  }
  return DEFAULT_LOCALE
}

// Top-level pages under a locale (next.config.mjs, from app/[locale]); empty means unchecked.
const ROUTES = new Set((process.env.HOSBAH_ROUTES ?? "").split(",").filter(Boolean))

export function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl
  if (
    pathname.startsWith("/api") ||
    // Webhooks (GitHub App, per-app deploy hooks) are the API's, rewritten there: no locale prefix.
    pathname.startsWith("/hooks/") ||
    // The CircleXO sign-in round trip (rewritten to the API): no locale prefix.
    pathname.startsWith("/auth/circlexo/") ||
    pathname === "/sso/circlexo" ||
    // The OAuth endpoints (MH-1153; MCP connectors: register, revoke) and the OAuth discovery
    // documents, rewritten to the API: no locale prefix. /oauth/authorize is the web's own page.
    pathname === "/oauth/token" ||
    pathname === "/oauth/register" ||
    pathname === "/oauth/revoke" ||
    pathname.startsWith("/.well-known/oauth-") ||
    pathname.startsWith("/_next") ||
    /\.[a-zA-Z0-9]+$/.test(pathname)
  ) {
    return NextResponse.next()
  }

  const [, first] = pathname.split("/")
  if (!isLocale(first)) {
    const url = request.nextUrl.clone()
    // An OAuth client names the consent page's language with ui_locales (space-separated, in order).
    const asked = pathname === "/oauth/authorize" ? (url.searchParams.get("ui_locales") ?? "").split(" ").find(isLocale) : undefined
    url.pathname = `/${asked ?? preferred(request)}${pathname === "/" ? "" : pathname}`
    return NextResponse.redirect(url)
  }

  // An unknown page: render the not-found page, but with a real 404 (MH-1257).
  const [, , second] = pathname.split("/")
  const response = second && ROUTES.size > 0 && !ROUTES.has(second)
    ? NextResponse.rewrite(request.nextUrl, { status: 404 })
    : NextResponse.next()
  if (request.cookies.get(LOCALE_COOKIE)?.value !== first) {
    response.cookies.set(LOCALE_COOKIE, first, { path: "/", maxAge: 31536000, sameSite: "lax" })
  }
  return response
}

export const config = { matcher: "/:path*" }
