import { formatDistanceToNow } from "date-fns";
import { enUS as dateFnsEnUS, zhCN as dateFnsZhCN } from "date-fns/locale";

import { detectLocale, type Locale } from "@/core/i18n";
import { getLocaleFromCookie } from "@/core/i18n/cookies";

function getDateFnsLocale(locale: Locale) {
  switch (locale) {
    case "zh-CN":
      return dateFnsZhCN;
    case "en-US":
    default:
      return dateFnsEnUS;
  }
}

export function formatTimeAgo(date: Date | string | number | undefined, locale?: Locale) {
  if (!date) return "";

  try {
    const effectiveLocale =
      locale ??
      (getLocaleFromCookie() as Locale | null) ??
      // Fallback when cookie is missing (or on first render)
      detectLocale();
    return formatDistanceToNow(new Date(date), {
      addSuffix: true,
      locale: getDateFnsLocale(effectiveLocale),
    });
  } catch (error) {
    return "";
  }
}
