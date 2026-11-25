import "@fontsource-variable/inter";
import "@fontsource-variable/jetbrains-mono";
import { reportStartupLoadFailure } from "@/lib/chunk-recovery";
import { appRoute } from "@/lib/app-path";
import { bootstrapAppearance } from "@/services/appearance-bootstrap";
import { isIsolatedPrevisRepro } from "@/lib/dev-repro";

// The public film entry checks its availability independently of workspace bootstrap.
const requestedRoute = appRoute(window.location.pathname);
if (/^\/welcome\/?$/.test(requestedRoute)) {
    void bootstrapAppearance().catch(() => undefined);
    void import("./welcome-application").catch(reportStartupLoadFailure);
} else {
    // The backend-free DEV lab must not make requests before AppProviders isolates it.
    const appearanceReady = isIsolatedPrevisRepro(import.meta.env.DEV, requestedRoute) ? Promise.resolve() : bootstrapAppearance();
    void import("./application").catch(reportStartupLoadFailure);
    void appearanceReady.catch(() => undefined);
}
