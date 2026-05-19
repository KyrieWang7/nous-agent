import { cn } from "@/lib/utils";

export function NousLogo({ className }: { className?: string }) {
    return (
        <>
            <img
                src="/nous.png"
                alt="Nous Logo"
                className={cn("block dark:hidden object-contain rounded-full shadow-sm", className)}
            />
            <img
                src="/nous-night.png"
                alt="Nous Logo"
                className={cn("hidden dark:block object-contain rounded-full shadow-sm", className)}
            />
        </>
    );
}
