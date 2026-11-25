"use client";

import { ChevronRightIcon } from "lucide-react";
import Link from "next/link";

import { Button } from "@/components/ui/button";
import { FlickeringGrid } from "@/components/ui/flickering-grid";
import Galaxy from "@/components/ui/galaxy";
import { WordRotate } from "@/components/ui/word-rotate";
import { cn } from "@/lib/utils";

export function Hero({ className }: { className?: string }) {
  return (
    <div
      className={cn(
        "flex size-full flex-col items-center justify-center",
        className,
      )}
    >
      <div aria-hidden="true" className="pointer-events-none absolute inset-0 z-0 bg-black/40">
        <Galaxy
          mouseRepulsion={false}
          starSpeed={0.2}
          density={0.6}
          glowIntensity={0.35}
          twinkleIntensity={0.3}
          speed={0.5}
        />
      </div>
      <FlickeringGrid
        className="absolute inset-0 z-0 mask-[url(/images/nous-line-art.svg)] mask-size-[120vw] mask-center mask-no-repeat md:mask-size-[90vh]"
        squareSize={4}
        gridGap={4}
        color={"white"}
        maxOpacity={0.3}
        flickerChance={0.25}
      />
      <div className="container-md relative z-10 mx-auto flex min-h-svh flex-col items-center justify-center px-4 pt-24 pb-16">
        <h1 className="flex flex-col items-center gap-2 text-center text-3xl font-bold sm:text-4xl lg:flex-row lg:text-6xl">
          <WordRotate
            words={[
              "Deep Research",
              "Collect Data",
              "Analyze Data",
              "Generate Webpages",
              "Vibe Coding",
              "Generate Slides",
              "Generate Images",
              "Generate Podcasts",
              "Generate Videos",
              "Generate Songs",
              "Organize Emails",
              "Do Anything",
              "Learn Anything",
            ]}
          />{" "}
          <span>with Nous</span>
        </h1>
        <p
          className="mt-8 max-w-3xl text-center text-lg leading-relaxed text-shadow-sm sm:text-2xl"
          style={{ color: "rgb(184,184,192)" }}
        >
          An open-source SuperAgent harness that researches, codes, and creates.
          With
          <br />
          the help of sandboxes, memories, tools, skills and subagents, it
          handles
          <br />
          different levels of tasks that could take minutes to hours.
        </p>
        <Link href="/workspace/chats/new">
          <Button
            className="group relative mt-12 overflow-hidden rounded-full bg-white px-12 py-8 text-black transition-all duration-300 hover:scale-[1.02] hover:bg-zinc-100 hover:shadow-[0_0_40px_rgba(255,255,255,0.4)]"
            size="lg"
          >
            <span className="relative z-10 flex items-center gap-3 text-2xl font-black uppercase tracking-[0.2em]">
              START
              <ChevronRightIcon className="size-8 transition-transform group-hover:translate-x-2" />
            </span>
          </Button>
        </Link>
        <Link href="/creative/" className="mt-5 rounded-md px-3 py-2 text-base text-white/75 underline-offset-4 hover:text-white hover:underline focus-visible:outline-2 focus-visible:outline-offset-4">
          进入内容工作台
        </Link>
      </div>
    </div>
  );
}
