"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { useAuthHydrated, useAuthStore } from "@/core/auth";

const TOONFLOW_SHELL_PATH = "/toonflow/index.html";
const TOONFLOW_BRIDGE_TOKEN = "Bearer nous-auth-bridge";
const DRAMA_WS_BASE_URL = process.env.NEXT_PUBLIC_DRAMA_WS_BASE_URL;
const DRAMA_HTTP_BASE_PATH = "/drama-api";

function buildDefaultWsBaseUrl() {
    if (typeof window === "undefined") {
        return DRAMA_HTTP_BASE_PATH;
    }

    const wsProtocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const { hostname, host } = window.location;

    // Next's route handlers proxy HTTP well, but not raw WebSocket upgrades.
    // For local development, connect the legacy Toonflow WS client directly
    // to the drama backend.
    if (hostname === "127.0.0.1" || hostname === "localhost") {
        return `${wsProtocol}//127.0.0.1:8003/api/drama`;
    }

    return `${wsProtocol}//${host}/api/drama`;
}

function buildToonflowUrl() {
    if (typeof window === "undefined") {
        return TOONFLOW_SHELL_PATH;
    }

    const wsBaseUrl = DRAMA_WS_BASE_URL ?? buildDefaultWsBaseUrl();
    const params = new URLSearchParams({
        baseUrl: `${window.location.origin}${DRAMA_HTTP_BASE_PATH}`,
        wsBaseUrl,
    });

    return `${TOONFLOW_SHELL_PATH}?${params.toString()}`;
}

function toLegacyUserId(userId: string | null | undefined) {
    if (!userId) {
        return "1";
    }

    let hash = 0;
    for (let index = 0; index < userId.length; index += 1) {
        hash = (hash * 31 + userId.charCodeAt(index)) % 1000000;
    }

    return String(hash || 1);
}

export function ToonflowShell() {
    const router = useRouter();
    const hydrated = useAuthHydrated();
    const isLoggedIn = useAuthStore((state) => state.isLoggedIn);
    const user = useAuthStore((state) => state.user);
    const [src, setSrc] = useState<string | null>(null);

    useEffect(() => {
        if (!hydrated) {
            return;
        }

        if (!isLoggedIn) {
            router.replace("/login");
            return;
        }

        window.localStorage.setItem("token", TOONFLOW_BRIDGE_TOKEN);
        window.localStorage.setItem("userId", toLegacyUserId(user?.id));
        setSrc(buildToonflowUrl());
    }, [hydrated, isLoggedIn, router, user?.id]);

    if (!hydrated || !isLoggedIn || !src) {
        return <div className="h-screen w-full bg-white" />;
    }

    return (
        <div className="h-screen w-full bg-white">
            <iframe
                key={src}
                src={src}
                title="Toonflow"
                className="h-full w-full border-0"
            />
        </div>
    );
}
