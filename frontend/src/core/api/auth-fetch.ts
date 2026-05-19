/**
 * Fetch wrapper. Auth has been removed for the open-source build.
 */
export async function authFetch(
    url: string | URL | Request,
    options?: RequestInit,
): Promise<Response> {
    return fetch(url, {
        ...options,
        credentials: options?.credentials ?? "include",
    });
}
