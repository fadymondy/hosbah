"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState, type FormEvent } from "react";
import { CheckCircle2Icon, InfoIcon, LoaderIcon } from "lucide-react";

import { PageBody } from "@/components/app-shell";
import { CopyField } from "@/components/copy-field";
import { FormError, useErrorText } from "@/components/form-error";
import { CheckBadge } from "@/components/server-status";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
  FieldTitle,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { apiFetch } from "@/lib/api";
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n";
import { useEventStream, type Install, type ProviderOption, type Server } from "@/lib/servers";
import { useTeam } from "@/lib/team";

const NAME = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;
const SIZES = ["small", "medium", "large"] as const;

export default function NewServerPage() {
  const { t, locale } = useI18n();
  const { team, manage } = useTeam();
  useDocumentTitle(`${t("servers.new.title")} — ${team.name}`);
  const router = useRouter();
  const fmt = useFormatDate();
  const errorText = useErrorText();
  const base = `/${locale}/t/${team.slug}`;

  const [providers, setProviders] = useState<ProviderOption[] | null>(null);
  const [source, setSource] = useState<"byo" | "proxmox">("byo");
  const [name, setName] = useState("");
  const [credential, setCredential] = useState("");
  const [kind, setKind] = useState<"vm" | "lxc">("lxc");
  const [size, setSize] = useState<(typeof SIZES)[number]>("small");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<{ server: Server; install: Install } | null>(null);
  const [live, setLive] = useState<Server | null>(null);

  useEffect(() => {
    apiFetch<{ providers: ProviderOption[] }>(`/api/teams/${team.slug}/server-providers`)
      .then((r) => {
        setProviders(r.providers);
        const px = r.providers.find((p) => p.key === "proxmox");
        if (px?.credentials[0]) setCredential(px.credentials[0].id);
      })
      .catch((err) => setError(errorText(err)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug]);

  // After the install command is shown, follow the server live: the page flips to "enrolled" by itself.
  useEventStream(created ? `/api/teams/${team.slug}/servers/${created.server.id}/stream` : null, {
    server: (d) => setLive(d as Server),
  });

  const proxmox = providers?.find((p) => p.key === "proxmox");
  const nameOK = NAME.test(name) && name.length <= 63;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!nameOK) return;
    setBusy(true);
    setError(null);
    try {
      if (source === "byo") {
        const res = await apiFetch<{ server: Server; install: Install }>(
          `/api/teams/${team.slug}/servers`,
          {
            method: "POST",
            body: { name, provider: "byo" },
          },
        );
        setCreated(res);
        setLive(res.server);
      } else {
        const res = await apiFetch<{ server: Server }>(`/api/teams/${team.slug}/servers`, {
          method: "POST",
          body: { name, provider: "proxmox", credential_id: credential, kind, size },
        });
        router.push(`${base}/servers/${res.server.id}`);
      }
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  if (!manage) {
    return (
      <PageBody title={t("servers.new.title")} description={t("servers.new.description")}>
        <Alert>
          <InfoIcon />
          <AlertDescription>{t("common.readOnly")}</AlertDescription>
        </Alert>
      </PageBody>
    );
  }

  if (created) {
    const server = live ?? created.server;
    const enrolled = server.status === "enrolled";
    return (
      <PageBody title={t("servers.new.title")} description={t("servers.new.description")}>
        {enrolled ? (
          <section className="flex flex-col gap-4 border p-4" data-testid="enrolled">
            <div className="flex items-center gap-2 font-medium">
              <CheckCircle2Icon className="size-5 text-primary" aria-hidden />
              {t("servers.new.enrolledTitle")}
            </div>
            <p className="text-sm text-muted-foreground">
              {t("servers.new.enrolledHint", { name: server.name })}
            </p>
            <ul className="flex flex-col gap-2 text-sm">
              {server.checks.map((c) => (
                <li key={c.name} className="flex flex-wrap items-center gap-2">
                  <CheckBadge status={c.status} />
                  <span className="font-medium">
                    {t(`servers.check.names.${c.name}`, undefined, c.name)}
                  </span>
                  <span dir="auto" className="text-muted-foreground">
                    {c.detail}
                  </span>
                </li>
              ))}
            </ul>
            <div>
              <Button nativeButton={false} render={<Link href={`${base}/servers/${server.id}`} />}>
                {t("servers.new.open")}
              </Button>
            </div>
          </section>
        ) : (
          <section className="flex flex-col gap-4 border p-4" data-testid="install">
            <div className="flex flex-col gap-1">
              <h2 className="font-medium">{t("servers.new.installTitle")}</h2>
              <p className="text-sm text-muted-foreground">{t("servers.new.installHint")}</p>
            </div>
            <CopyField value={created.install.command} />
            <div className="flex flex-col gap-1">
              <p className="text-sm text-muted-foreground">{t("servers.new.installRoot")}</p>
              <CopyField value={created.install.command_root} />
            </div>
            <p className="text-sm text-muted-foreground">
              {t("servers.new.expires", { when: fmt(created.install.expires_at) })}
            </p>
            <p className="flex items-center gap-2 text-sm" role="status" aria-live="polite">
              <LoaderIcon className="size-4 animate-spin" aria-hidden />
              {t("servers.new.waiting")}
            </p>
          </section>
        )}
      </PageBody>
    );
  }

  return (
    <PageBody title={t("servers.new.title")} description={t("servers.new.description")}>
      <div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]">
        <form onSubmit={submit} className="flex min-w-0 flex-col gap-6">
          <FieldSet>
            <FieldLegend variant="label">{t("servers.new.source")}</FieldLegend>
            <RadioGroup value={source} onValueChange={(v) => setSource(v as "byo" | "proxmox")}>
              <FieldLabel htmlFor="src-byo">
                <Field orientation="horizontal">
                  <RadioGroupItem value="byo" id="src-byo" />
                  <FieldContent>
                    <FieldTitle>{t("servers.new.byoTitle")}</FieldTitle>
                    <FieldDescription>{t("servers.new.byoHint")}</FieldDescription>
                  </FieldContent>
                </Field>
              </FieldLabel>
              <FieldLabel htmlFor="src-proxmox">
                <Field orientation="horizontal">
                  <RadioGroupItem value="proxmox" id="src-proxmox" />
                  <FieldContent>
                    <FieldTitle>{t("servers.new.proxmoxTitle")}</FieldTitle>
                    <FieldDescription>{t("servers.new.proxmoxHint")}</FieldDescription>
                  </FieldContent>
                </Field>
              </FieldLabel>
            </RadioGroup>
          </FieldSet>

          {source === "proxmox" && proxmox && !proxmox.available ? (
            <Alert data-testid="proxmox-not-configured">
              <InfoIcon />
              <AlertTitle>{t("servers.new.notConfiguredTitle")}</AlertTitle>
              <AlertDescription className="flex flex-col items-start gap-3">
                <span>{t("servers.new.notConfiguredHint")}</span>
                <Button
                  size="sm"
                  variant="outline"
                  nativeButton={false}
                  render={<Link href={`${base}/credentials`} />}
                >
                  {t("servers.new.addCredential")}
                </Button>
              </AlertDescription>
            </Alert>
          ) : (
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="server-name">{t("servers.name")}</FieldLabel>
                <Input
                  id="server-name"
                  dir="ltr"
                  required
                  maxLength={63}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  aria-invalid={name !== "" && !nameOK}
                  autoComplete="off"
                />
                <FieldDescription>{t("servers.new.nameHint")}</FieldDescription>
              </Field>
              {source === "proxmox" && proxmox ? (
                <>
                  <Field>
                    <FieldLabel htmlFor="px-cred">{t("servers.new.credential")}</FieldLabel>
                    <Select
                      value={credential}
                      onValueChange={(v) => v && setCredential(v as string)}
                    >
                      <SelectTrigger id="px-cred" className="w-full">
                        <SelectValue>
                          {(v: string) => proxmox.credentials.find((c) => c.id === v)?.name ?? ""}
                        </SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        {proxmox.credentials.map((c) => (
                          <SelectItem key={c.id} value={c.id}>
                            {c.name}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="px-kind">{t("servers.new.kind")}</FieldLabel>
                    <Select value={kind} onValueChange={(v) => v && setKind(v as "vm" | "lxc")}>
                      <SelectTrigger id="px-kind" className="w-full">
                        <SelectValue>{(v: string) => t(`servers.kinds.${v}`)}</SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="lxc">{t("servers.kinds.lxc")}</SelectItem>
                        <SelectItem value="vm">{t("servers.kinds.vm")}</SelectItem>
                      </SelectContent>
                    </Select>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="px-size">{t("servers.new.size")}</FieldLabel>
                    <Select
                      value={size}
                      onValueChange={(v) => v && setSize(v as (typeof SIZES)[number])}
                    >
                      <SelectTrigger id="px-size" className="w-full">
                        <SelectValue>{(v: string) => t(`servers.new.sizes.${v}`)}</SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        {SIZES.map((s) => (
                          <SelectItem key={s} value={s}>
                            {t(`servers.new.sizes.${s}`)}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                </>
              ) : null}
              <FormError message={error} />
              <div>
                <Button type="submit" disabled={busy || !nameOK || providers === null}>
                  {busy
                    ? t("common.working")
                    : source === "byo"
                      ? t("servers.new.createByo")
                      : t("servers.new.createProxmox")}
                </Button>
              </div>
            </FieldGroup>
          )}
        </form>
        <HowItWorks />
      </div>
    </PageBody>
  );
}

function HowItWorks() {
  const { t } = useI18n();
  return (
    <aside className="flex h-fit flex-col border" aria-labelledby="how-h">
      <h2 id="how-h" className="grid-rule-bottom px-4 py-3 font-medium">
        {t("servers.new.howTitle")}
      </h2>
      <ol className="flex flex-col divide-y text-sm">
        {(["how1", "how2", "how3"] as const).map((k, i) => (
          <li key={k} className="flex gap-3 px-4 py-3">
            <span className="font-mono text-muted-foreground tabular-nums">
              {String(i + 1).padStart(2, "0")}
            </span>
            <span className="text-muted-foreground">{t(`servers.new.${k}`)}</span>
          </li>
        ))}
      </ol>
    </aside>
  );
}
