"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState, type FormEvent } from "react";
import { CheckCircle2Icon, InfoIcon, LoaderIcon } from "lucide-react";

import { PageBody } from "@/components/app-shell";
import { CopyField } from "@/components/copy-field";
import { FormError, useErrorText } from "@/components/form-error";
import { Notice } from "@/components/states";
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
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Textarea } from "@/components/ui/textarea";
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
type Source = "byo" | "connect" | "proxmox";

export default function NewServerPage() {
  const { t, locale } = useI18n();
  const { team, manage } = useTeam();
  useDocumentTitle(`${t("servers.new.title")} — ${team.name}`);
  const router = useRouter();
  const fmt = useFormatDate();
  const errorText = useErrorText();
  const base = `/${locale}/t/${team.slug}`;

  const [providers, setProviders] = useState<ProviderOption[] | null>(null);
  const [source, setSource] = useState<Source>("byo");
  const [host, setHost] = useState("");
  const [port, setPort] = useState("22");
  const [user, setUser] = useState("root");
  const [auth, setAuth] = useState<"password" | "key">("password");
  const [password, setPassword] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [registerProxmox, setRegisterProxmox] = useState(true);
  const [name, setName] = useState("");
  const [credential, setCredential] = useState("");
  const [kind, setKind] = useState<"vm" | "lxc">("lxc");
  const [size, setSize] = useState<(typeof SIZES)[number]>("small");
  const [ipconfig, setIpconfig] = useState("");
  const [nameserver, setNameserver] = useState("");
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
  const connectOK =
    host.trim() !== "" &&
    user.trim() !== "" &&
    (auth === "password" ? password !== "" : privateKey.trim() !== "");

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
      } else if (source === "connect") {
        const res = await apiFetch<{ server: Server }>(`/api/teams/${team.slug}/servers`, {
          method: "POST",
          body: {
            name,
            provider: "byo",
            connect: {
              host: host.trim(),
              port: Number(port) || 22,
              user: user.trim(),
              auth,
              ...(auth === "password" ? { password } : { private_key: privateKey }),
              register_proxmox: registerProxmox,
            },
          },
        });
        router.push(`${base}/servers/${res.server.id}`);
      } else {
        const res = await apiFetch<{ server: Server }>(`/api/teams/${team.slug}/servers`, {
          method: "POST",
          body: {
            name,
            provider: "proxmox",
            credential_id: credential,
            kind,
            size,
            ...(ipconfig.trim() ? { ipconfig: ipconfig.trim() } : {}),
            ...(nameserver.trim() ? { nameserver: nameserver.trim() } : {}),
          },
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
      <PageBody title={t("servers.new.title")} description={t("servers.new.description")} bleed>
        <Notice icon={<InfoIcon />}>{t("common.readOnly")}</Notice>
      </PageBody>
    );
  }

  if (created) {
    const server = live ?? created.server;
    const enrolled = server.status === "enrolled";
    return (
      <PageBody title={t("servers.new.title")} description={t("servers.new.description")} bleed>
        {enrolled ? (
          <section className="flex flex-col gap-4 border-b border-line px-6 py-6 [&>*]:max-w-3xl" data-testid="enrolled">
            <div className="flex items-center gap-2 font-medium">
              <CheckCircle2Icon className="size-5 text-grid-brand" aria-hidden />
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
          <section className="flex flex-col gap-4 border-b border-line px-6 py-6 [&>*]:max-w-3xl" data-testid="install">
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
    <PageBody title={t("servers.new.title")} description={t("servers.new.description")} bleed>
      <div className="grid gap-px border-b border-line bg-line lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]">
        <form onSubmit={submit} className="flex min-w-0 flex-col gap-6 bg-grid-bg px-6 py-6 [&>*]:max-w-2xl">
          <FieldSet>
            <FieldLegend variant="label">{t("servers.new.source")}</FieldLegend>
            <RadioGroup value={source} onValueChange={(v) => setSource(v as Source)}>
              <FieldLabel htmlFor="src-byo">
                <Field orientation="horizontal">
                  <RadioGroupItem value="byo" id="src-byo" />
                  <FieldContent>
                    <FieldTitle>{t("servers.new.byoTitle")}</FieldTitle>
                    <FieldDescription>{t("servers.new.byoHint")}</FieldDescription>
                  </FieldContent>
                </Field>
              </FieldLabel>
              <FieldLabel htmlFor="src-connect">
                <Field orientation="horizontal">
                  <RadioGroupItem value="connect" id="src-connect" />
                  <FieldContent>
                    <FieldTitle>{t("servers.new.connectTitle")}</FieldTitle>
                    <FieldDescription>{t("servers.new.connectHint")}</FieldDescription>
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
              {source === "connect" ? (
                <ConnectFields
                  {...{ host, setHost, port, setPort, user, setUser, auth, setAuth }}
                  {...{
                    password,
                    setPassword,
                    privateKey,
                    setPrivateKey,
                    registerProxmox,
                    setRegisterProxmox,
                  }}
                />
              ) : null}
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
                  <Field>
                    <FieldLabel htmlFor="px-ip">{t("servers.new.ipconfig")}</FieldLabel>
                    <Input
                      id="px-ip"
                      dir="ltr"
                      placeholder="ip=10.10.10.121/24,gw=10.10.10.1"
                      value={ipconfig}
                      onChange={(e) => setIpconfig(e.target.value)}
                      autoComplete="off"
                    />
                    <FieldDescription>{t("servers.new.ipconfigHint")}</FieldDescription>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="px-ns">{t("servers.new.nameserver")}</FieldLabel>
                    <Input
                      id="px-ns"
                      dir="ltr"
                      placeholder="1.1.1.1"
                      value={nameserver}
                      onChange={(e) => setNameserver(e.target.value)}
                      autoComplete="off"
                    />
                  </Field>
                </>
              ) : null}
              <FormError message={error} />
              <div>
                <Button
                  type="submit"
                  disabled={
                    busy || !nameOK || providers === null || (source === "connect" && !connectOK)
                  }
                >
                  {busy
                    ? t("common.working")
                    : source === "byo"
                      ? t("servers.new.createByo")
                      : source === "connect"
                        ? t("servers.new.createConnect")
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
    <aside className="flex flex-col bg-grid-bg" aria-labelledby="how-h">
      <h2 id="how-h" className="border-b border-line px-6 pt-5 pb-3 text-base font-medium text-grid-fg">
        {t("servers.new.howTitle")}
      </h2>
      <ol className="flex flex-col divide-y divide-line border-b border-line text-sm lg:border-b-0">
        {(["how1", "how2", "how3"] as const).map((k, i) => (
          <li key={k} className="flex gap-3 px-6 py-3">
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

type Setter<T> = (v: T) => void;

function ConnectFields(p: {
  host: string;
  setHost: Setter<string>;
  port: string;
  setPort: Setter<string>;
  user: string;
  setUser: Setter<string>;
  auth: "password" | "key";
  setAuth: Setter<"password" | "key">;
  password: string;
  setPassword: Setter<string>;
  privateKey: string;
  setPrivateKey: Setter<string>;
  registerProxmox: boolean;
  setRegisterProxmox: Setter<boolean>;
}) {
  const { t } = useI18n();
  return (
    <>
      <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_8rem]">
        <Field>
          <FieldLabel htmlFor="c-host">{t("servers.new.host")}</FieldLabel>
          <Input
            id="c-host"
            dir="ltr"
            required
            maxLength={253}
            value={p.host}
            onChange={(e) => p.setHost(e.target.value)}
            autoComplete="off"
            placeholder="203.0.113.10"
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="c-port">{t("servers.new.port")}</FieldLabel>
          <Input
            id="c-port"
            dir="ltr"
            inputMode="numeric"
            value={p.port}
            onChange={(e) => p.setPort(e.target.value.replace(/\D/g, "").slice(0, 5))}
          />
        </Field>
      </div>
      <Field>
        <FieldLabel htmlFor="c-user">{t("servers.new.user")}</FieldLabel>
        <Input
          id="c-user"
          dir="ltr"
          required
          maxLength={64}
          value={p.user}
          onChange={(e) => p.setUser(e.target.value)}
          autoComplete="off"
        />
        <FieldDescription>{t("servers.new.userHint")}</FieldDescription>
      </Field>
      <FieldSet>
        <FieldLegend variant="label">{t("servers.new.auth")}</FieldLegend>
        <RadioGroup
          value={p.auth}
          onValueChange={(v) => p.setAuth(v as "password" | "key")}
          className="flex flex-wrap gap-6"
        >
          <Field orientation="horizontal" className="w-auto">
            <RadioGroupItem value="password" id="c-auth-pw" />
            <FieldLabel htmlFor="c-auth-pw">{t("servers.new.authPassword")}</FieldLabel>
          </Field>
          <Field orientation="horizontal" className="w-auto">
            <RadioGroupItem value="key" id="c-auth-key" />
            <FieldLabel htmlFor="c-auth-key">{t("servers.new.authKey")}</FieldLabel>
          </Field>
        </RadioGroup>
      </FieldSet>
      {p.auth === "password" ? (
        <Field>
          <FieldLabel htmlFor="c-pw">{t("servers.new.password")}</FieldLabel>
          <Input
            id="c-pw"
            type="password"
            dir="ltr"
            required
            value={p.password}
            onChange={(e) => p.setPassword(e.target.value)}
            autoComplete="off"
          />
          <FieldDescription>{t("servers.new.secretHint")}</FieldDescription>
        </Field>
      ) : (
        <Field>
          <FieldLabel htmlFor="c-key">{t("servers.new.privateKey")}</FieldLabel>
          <Textarea
            id="c-key"
            dir="ltr"
            required
            rows={6}
            className="font-mono text-xs"
            value={p.privateKey}
            onChange={(e) => p.setPrivateKey(e.target.value)}
            placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
            spellCheck={false}
          />
          <FieldDescription>
            {t("servers.new.privateKeyHint")} {t("servers.new.secretHint")}
          </FieldDescription>
        </Field>
      )}
      <Field orientation="horizontal">
        <Checkbox
          id="c-pve"
          checked={p.registerProxmox}
          onCheckedChange={(v) => p.setRegisterProxmox(v === true)}
        />
        <FieldContent>
          <FieldLabel htmlFor="c-pve">{t("servers.new.registerProxmox")}</FieldLabel>
          <FieldDescription>{t("servers.new.registerProxmoxHint")}</FieldDescription>
        </FieldContent>
      </Field>
    </>
  );
}
