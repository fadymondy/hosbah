import { BrandLoader } from "@/components/brand-loader"

/** Route-level fallback while a segment streams in. */
export default function Loading() {
  return (
    <div className="flex min-h-dvh items-center justify-center" role="status" aria-busy>
      <BrandLoader size={96} />
    </div>
  )
}
