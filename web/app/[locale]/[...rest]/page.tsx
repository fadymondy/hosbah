import { notFound } from "next/navigation"

/** Any unknown path under a locale renders [locale]/not-found.tsx (the signed-out shell). */
export default function CatchAll() {
  notFound()
}
