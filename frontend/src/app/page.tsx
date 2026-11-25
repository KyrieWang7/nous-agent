import { Footer } from "@/components/landing/footer";
import { Header } from "@/components/landing/header";
import { Hero } from "@/components/landing/hero";
import { CommunitySection } from "@/components/landing/sections/community-section";
import { SandboxSection } from "@/components/landing/sections/sandbox-section";
import { SkillsSection } from "@/components/landing/sections/skills-section";
import { WhatsNewSection } from "@/components/landing/sections/whats-new-section";

export default function RootPage() {
  return (
    <div className="dark min-h-dvh overflow-x-clip bg-[#0a0a0a] text-foreground">
      <Header />
      <main>
        <section className="relative isolate overflow-hidden" aria-label="Nous">
          <Hero />
        </section>
        <div id="features" className="container-md scroll-mt-20 px-4 sm:px-8">
          <WhatsNewSection />
          <SkillsSection />
          <SandboxSection />
          <CommunitySection />
        </div>
      </main>
      <Footer />
    </div>
  );
}
