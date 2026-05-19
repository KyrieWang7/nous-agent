/**
 * Novel Parser Utility
 * Parses raw novel text into structured chapter/reel format
 */

const REEL_REGEX = /^(第[\d一二三四五六七八九十百千]+卷)\s*([^\n第]*)/gm;
const CHAPTER_REGEX = /(第[\d一二三四五六七八九十百千]+章)\s*([^\n\r]*)/g;

const CHINESE_NUM_MAP: Record<string, number> = {
  零: 0,
  一: 1,
  二: 2,
  三: 3,
  四: 4,
  五: 5,
  六: 6,
  七: 7,
  八: 8,
  九: 9,
};

const CHINESE_UNIT_MAP: Record<string, number> = {
  十: 10,
  百: 100,
  千: 1000,
};

export interface ParsedChapter {
  index: number;
  chapter: string;
  text: string;
}

export interface ParsedReel {
  index: number;
  reel: string;
  chapters: ParsedChapter[];
}

function parseNumber(numStr: string): number {
  if (/^\d+$/.test(numStr)) return parseInt(numStr, 10);

  if (/^十[一二三四五六七八九]?$/.test(numStr)) {
    if (numStr.length === 1) return 10;
    const secondChar = numStr[1];
    if (secondChar === undefined) return 10;
    return 10 + (CHINESE_NUM_MAP[secondChar] ?? 0);
  }

  let num = 0;
  let digit = 0;

  for (const c of numStr) {
    if (CHINESE_NUM_MAP[c] !== undefined) {
      digit = CHINESE_NUM_MAP[c];
    } else if (CHINESE_UNIT_MAP[c] !== undefined) {
      if (digit === 0 && c === "十") digit = 1;
      num += digit * CHINESE_UNIT_MAP[c];
      digit = 0;
    }
  }

  num += digit;
  return num;
}

export function parseNovel(text: string): ParsedReel[] {
  REEL_REGEX.lastIndex = 0;
  const reelMatches = Array.from(text.matchAll(REEL_REGEX));
  const reels: ParsedReel[] = [];

  // No reel structure
  if (reelMatches.length === 0) {
    const chapters: ParsedChapter[] = [];
    CHAPTER_REGEX.lastIndex = 0;
    const matches = Array.from(text.matchAll(CHAPTER_REGEX));

    if (matches.length === 0 && text.trim() !== "") {
      chapters.push({ index: 1, chapter: "", text: text.trim() });
    } else {
      for (let i = 0; i < matches.length; i++) {
        const match = matches[i];
        if (!match) continue;
        if (match.index === undefined) continue;
        const match0 = match[0];
        const match1 = match[1];
        if (match0 === undefined || match1 === undefined) continue;

        const start = match.index + match0.length;
        const nextMatch = matches[i + 1];
        const end = nextMatch?.index ?? text.length;
        const content = text
          .slice(start, end)
          .replace(/^[\r\n]+/, "")
          .trim();

        const chapterNum = match1.replace(/第|章/g, "");
        const chapterName = match[2] ?? "";
        chapters.push({
          index: parseNumber(chapterNum),
          chapter: chapterName.trim(),
          text: content,
        });
      }
    }

    // Sort chapters by index
    chapters.sort((a, b) => a.index - b.index);

    reels.push({
      index: 1,
      reel: "正文卷",
      chapters,
    });

    return reels;
  }

  // Has reel structure
  const reelMap = new Map<string, ParsedReel>();

  for (let i = 0; i < reelMatches.length; i++) {
    const match = reelMatches[i];
    if (!match) continue;
    if (match.index === undefined) continue;
    const match0 = match[0];
    const match1 = match[1];
    if (match0 === undefined || match1 === undefined) continue;

    const index = match.index;
    const reelRaw = match1;
    const reelName = (match[2] ?? "").trim() || "";
    const nextMatch = reelMatches[i + 1];
    const end = nextMatch?.index ?? text.length;
    const reelSection = text.slice(index, end);

    const chapterMatches = Array.from(reelSection.matchAll(CHAPTER_REGEX));
    const chapters: ParsedChapter[] = [];

    const sectionWithoutReel = reelSection.replace(REEL_REGEX, "").trim();
    if (chapterMatches.length === 0 && sectionWithoutReel !== "") {
      chapters.push({
        index: 1,
        chapter: "",
        text: sectionWithoutReel,
      });
    }

    for (let j = 0; j < chapterMatches.length; j++) {
      const chapterMatch = chapterMatches[j];
      if (!chapterMatch) continue;
      if (chapterMatch.index === undefined) continue;
      const cm0 = chapterMatch[0];
      const cm1 = chapterMatch[1];
      if (cm0 === undefined || cm1 === undefined) continue;

      const start = chapterMatch.index + cm0.length;
      const nextChapterMatch = chapterMatches[j + 1];
      const endIdx = nextChapterMatch?.index ?? reelSection.length;
      const content = reelSection
        .slice(start, endIdx)
        .replace(/^[\r\n]+/, "")
        .trim();

      const chapterNum = cm1.replace(/第|章/g, "");
      const chapterName = chapterMatch[2] ?? "";
      chapters.push({
        index: parseNumber(chapterNum),
        chapter: chapterName.trim(),
        text: content,
      });
    }

    // Sort chapters within reel
    chapters.sort((a, b) => a.index - b.index);

    if (!reelMap.has(reelName)) {
      reelMap.set(reelName, {
        index: parseNumber(reelRaw.replace(/第|卷/g, "")),
        reel: reelName,
        chapters: [],
      });
    }
    const existingReel = reelMap.get(reelName);
    if (existingReel) {
      existingReel.chapters.push(...chapters);
    }
  }

  // Sort reels by index
  const result = Array.from(reelMap.values()).sort((a, b) => a.index - b.index);
  result.forEach((reel) => reel.chapters.sort((a, b) => a.index - b.index));

  return result;
}
