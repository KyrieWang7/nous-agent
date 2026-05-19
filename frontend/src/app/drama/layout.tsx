"use client";

/**
 * Drama Layout - Main layout for short drama generation pages
 */
export default function DramaLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <div className="flex-1 overflow-auto bg-background">
      {children}
    </div>
  );
}
