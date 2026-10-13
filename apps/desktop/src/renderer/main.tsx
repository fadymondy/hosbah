import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles/app.css";
import { AppProvider, useApp } from "./lib/app";
import { Spinner } from "./components/ui";
import { Alerts } from "./views/Alerts";
import { AppDetail } from "./views/AppDetail";
import { Apps } from "./views/Apps";
import { Backups } from "./views/Backups";
import { Domains } from "./views/Domains";
import { Login } from "./views/Login";
import { Overview } from "./views/Overview";
import { ServerDetail } from "./views/ServerDetail";
import { Settings } from "./views/Settings";
import { Shell } from "./views/Shell";
import { TrayPreview } from "./views/TrayPreview";

function Screen() {
  const { route } = useApp();
  switch (route.section) {
    case "apps":
      return route.id ? <AppDetail key={route.id} /> : <Apps />;
    case "servers":
      return route.id ? <ServerDetail key={route.id} /> : <Overview />;
    case "domains":
      return <Domains />;
    case "backups":
      return <Backups />;
    case "alerts":
      return <Alerts />;
    case "settings":
      return <Settings />;
    default:
      return <Overview />;
  }
}

function Root() {
  const { ready, session, route } = useApp();
  if (!ready) return <Spinner />;
  if (route.section === "tray-preview") return <TrayPreview />;
  if (!session?.signedIn) return <Login />;
  return (
    <Shell>
      <Screen />
    </Shell>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AppProvider>
      <Root />
    </AppProvider>
  </StrictMode>,
);
