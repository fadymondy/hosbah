import { LogoLoader } from "@fadymondy/nasaq/web"

/** The Hosbah mark as a loader: Nasaq's LogoLoader around the official mark. */
export function BrandLoader({ size = 56, className = "" }: { size?: number; className?: string }) {
  return <LogoLoader size={size} className={className} />
}
