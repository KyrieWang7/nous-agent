import { useState, useEffect, useRef } from "react";

/**
 * Hook that returns elapsed time in seconds since mounting or since `startTime`.
 * Stops updating when `active` becomes false.
 */
export function useElapsed(active: boolean, startTime?: number): number {
    const start = useRef(startTime ?? Date.now());
    const [elapsed, setElapsed] = useState(0);

    useEffect(() => {
        if (!active) return;
        start.current = startTime ?? Date.now();
        const timer = setInterval(() => {
            setElapsed(Math.floor((Date.now() - start.current) / 1000));
        }, 1000);
        return () => clearInterval(timer);
    }, [active, startTime]);

    return active ? elapsed : elapsed;
}

/**
 * Format seconds to a human-readable string like "5s", "1m 23s", "2m 0s"
 */
export function formatElapsed(seconds: number): string {
    if (seconds < 60) return `${seconds}s`;
    const m = Math.floor(seconds / 60);
    const s = seconds % 60;
    return `${m}m ${s}s`;
}
