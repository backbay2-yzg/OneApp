import { MainApp } from "./main/MainApp.js";
import { LauncherApp } from "./launcher/LauncherApp.js";
import { TrayPopoverApp } from "./tray/TrayPopoverApp.js";

export default function App() {
  const hash = window.location.hash;
  if (hash.startsWith("#/launcher")) {
    return <LauncherApp />;
  }
  if (hash.startsWith("#/tray")) {
    return <TrayPopoverApp />;
  }
  return <MainApp />;
}
