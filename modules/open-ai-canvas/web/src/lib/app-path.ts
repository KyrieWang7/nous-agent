export const appBasePath = (import.meta.env.BASE_URL || "/").replace(/\/$/, "");

export function appPath(path: string): string {
    if (!path || !appBasePath || !path.startsWith("/") || path.startsWith("//") || path === appBasePath || path.startsWith(appBasePath + "/")) return path;
    return appBasePath + path;
}

export function appRoute(path: string): string {
    return appBasePath && path.startsWith(appBasePath + "/") ? path.slice(appBasePath.length) : path === appBasePath ? "/" : path;
}
