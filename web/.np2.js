const fs=require("fs");const p="app/[locale]/t/[team]/apps/new/page.tsx";let s=fs.readFileSync(p,"utf8").replace(/\r\n/g,"\n");
const a="    // eslint-disable-next-line react-hooks/exhaustive-deps\n  }, [team.slug])\n";
if(!s.includes(a))throw new Error("anchor");
s=s.replace(a,a+`
  // ?stack=<recipe> (from the catalog) prefills the repository and branch from that stack.
  const [stack, setStack] = useState<{ name: string; stack: { repo: string; branch: string; notes?: string; cloudy_toml?: string } } | null>(null)
  useEffect(() => {
    const id = new URLSearchParams(window.location.search).get("stack")
    if (!id) return
    apiFetch<{ name: string; stack: { repo: string; branch: string; notes?: string; cloudy_toml?: string } }>(\`/api/teams/\${team.slug}/catalog/\${encodeURIComponent(id)}\`)
      .then((r) => {
        if (!r.stack) return
        setStack(r)
        setRepo((cur) => cur || r.stack.repo)
        setBranch((cur) => (cur && cur !== "main" ? cur : r.stack.branch))
      })
      .catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])
`);
// a notice under the page's error band
const b=s.match(/<ErrorBand message=\{error\} \/>\n/);
if(!b)throw new Error("band");
s=s.replace(b[0],b[0]+`      {stack ? (
        <p className="border-b border-line bg-grid-card px-6 py-3 text-sm">
          {t("site.stacks.fromStack", { name: stack.name })}
          {stack.stack.notes ? <span className="block text-xs text-grid-muted">{stack.stack.notes}</span> : null}
        </p>
      ) : null}
`);
fs.writeFileSync(p,s);
for (const [l, v] of [["en", "Prefilled from the {name} stack."], ["ar", "مُلئ مسبقًا من حزمة {name}."]]) {
  const f = "lang/" + l + ".json"
  const j = JSON.parse(fs.readFileSync(f, "utf8"))
  j.site.stacks.fromStack = v
  fs.writeFileSync(f, JSON.stringify(j, null, 2) + "\n")
}
