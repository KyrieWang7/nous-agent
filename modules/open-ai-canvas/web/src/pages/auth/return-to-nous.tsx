import { useEffect } from "react";

import { FullScreenLoader } from "@/components/ui/aceternity/full-screen-loader";
import { nousOrigin } from "@/lib/auth-entry";
import { useUserStore } from "@/stores/use-user-store";

export function ReturnToNous() {
    const role = useUserStore((state) => state.user?.role);
    useEffect(() => {
        if (role && nousOrigin) window.location.replace(new URL(role === "admin" ? "/workspace/" : "/", nousOrigin));
    }, [role]);
    return <FullScreenLoader />;
}
