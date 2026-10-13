// A drawn copy of the tray menu (a native menu cannot be screenshotted). Reads the same model the
// real tray is built from, so it shows exactly what the menu says.
import clsx from "clsx";
import { useApp } from "../lib/app";
import { overallDot } from "../components/ui";

export function TrayPreview() {
  const { monitor } = useApp();
  const m = monitor?.tray;
  if (!m) return null;
  return (
    <div className="grid h-full place-items-center bg-bg p-8">
      <div className="w-80 rounded-xl border border-line bg-elevated py-2 text-sm text-fg shadow-2xl" role="menu">
        <div className="flex items-center gap-2 px-4 py-1.5 text-muted">
          <span className={clsx("size-3 rounded-full", overallDot[m.level])} />
          {m.statusLine}
        </div>
        <div className="px-4 py-1.5 text-muted">{m.unreadLine}</div>
        <hr className="my-1.5 border-line" />
        <div className="px-4 py-1.5 font-medium">{m.openLabel}</div>
        <hr className="my-1.5 border-line" />
        <div className="px-4 py-1 text-xs text-muted">{m.recentTitle}</div>
        {m.recent.length === 0 && <div className="px-4 py-1.5 text-muted">{m.emptyLine}</div>}
        {m.recent.map((r) => (
          <div key={r.id} className="truncate px-4 py-1.5 hover:bg-soft">
            {r.unread ? "● " : ""}
            {r.title}
          </div>
        ))}
        <hr className="my-1.5 border-line" />
        <div className="px-4 py-1.5">{m.quitLabel}</div>
      </div>
    </div>
  );
}
