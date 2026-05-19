/** 短剧生成布局 */
import type { ReactNode } from "react";

export default function DramaLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex-1 overflow-auto">
      {children}
    </div>
  );
}
