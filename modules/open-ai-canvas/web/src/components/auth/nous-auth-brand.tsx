import { nousLogoURL } from "@/lib/auth-entry";
import { useThemeStore } from "@/stores/use-theme-store";

export function NousAuthBrand({ origin }: { origin: string }) {
    const theme = useThemeStore((state) => state.theme);
    return (
        <a href={origin} className="inline-flex items-center gap-2 font-semibold text-foreground">
            <img src={nousLogoURL(origin, theme)} alt="" className="size-8 rounded-full object-contain" />
            <span>Nous</span>
        </a>
    );
}
