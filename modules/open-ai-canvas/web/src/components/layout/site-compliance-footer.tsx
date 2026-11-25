import { ExternalLink } from "lucide-react";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";
import { useAppearanceStore } from "@/stores/use-appearance-store";

const MIIT_FILING_URL = "https://beian.miit.gov.cn/";

export function SiteComplianceFooter({ className, variant = "default", showCopyright = true }: { className?: string; variant?: "default" | "auth"; showCopyright?: boolean }) {
    const { t } = useTranslation();
    const appearance = useAppearanceStore((state) => state.appearance);
    const filingNumber = appearance.icpFilingEnabled ? appearance.icpFilingNumber : "";
    if (!showCopyright && !filingNumber) return null;

    return (
        <footer className={cn("site-compliance-footer", variant === "auth" && "is-auth", className)} aria-label={t("account.layout.footer.label")}>
            {showCopyright ? <span>{appearance.footerCopyright}</span> : null}
            {filingNumber ? (
                <a href={MIIT_FILING_URL} target="_blank" rel="noopener noreferrer" aria-label={t("account.layout.footer.icpLink", { number: filingNumber })}>
                    {filingNumber}
                    <ExternalLink aria-hidden="true" />
                </a>
            ) : null}
        </footer>
    );
}
