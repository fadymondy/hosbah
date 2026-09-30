"use client"


import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@fadymondy/nasaq/web"
import { ROLES, type Role } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

export function RoleSelect({
  value,
  onChange,
  id,
  disabled,
  roles = ROLES,
  className,
}: {
  value: Role
  onChange: (role: Role) => void
  id?: string
  disabled?: boolean
  roles?: Role[]
  className?: string
}) {
  const { t } = useI18n()
  return (
    <Select value={value} onValueChange={(v) => v && onChange(v as Role)} disabled={disabled}>
      <SelectTrigger id={id} className={className ?? "w-36"} aria-label={t("members.role")}>
        <SelectValue>{(v: string) => t(`roles.${v}`)}</SelectValue>
      </SelectTrigger>
      <SelectContent>
        {roles.map((r) => (
          <SelectItem key={r} value={r}>
            {t(`roles.${r}`)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
