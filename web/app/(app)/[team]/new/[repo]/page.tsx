import type { Metadata } from "next";
import { ReviewForm } from "@/components/onboarding/review-form";
import { Steps } from "@/components/onboarding/steps";
import { DataSection } from "@/components/ui/data-section";
import { Skeleton } from "@/components/ui/skeleton";
import { Arrives } from "@/components/ui/arrives";
import { api, available } from "@/lib/api";

export const metadata: Metadata = { title: "Review and deploy" };

export function generateStaticParams() {
  return [{ repo: "url-shortener" }];
}

export default function ReviewPage({ params }: PageProps<"/[team]/new/[repo]">) {
  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-8">
      <Steps current={1} />
      <DataSection what="detected services" fallback={<Skeleton className="h-[480px] w-full rounded-panel" />}>
        <Content params={params} />
      </DataSection>
    </div>
  );
}

async function Content({ params }: { params: PageProps<"/[team]/new/[repo]">["params"] }) {
  if (!available.repos) return <Arrives title="Import from GitHub" when="GitHub deploys, the next step" command="grey deploy" />;
  const { team, repo } = await params;
  const detected = await api.detectServices(team, repo);
  return (
    <div className="flex flex-col gap-8">
      <div className="flex flex-col gap-1">
        <h1 className="text-28 font-semibold tracking-tight">
          We found {detected.length} {detected.length === 1 ? "service" : "services"}
        </h1>
        <p className="text-muted">
          <span className="font-mono text-13 text-fg">haki/{repo}</span> · branch <span className="font-mono text-13 text-fg">main</span> · go1.27 from go.mod
        </p>
      </div>
      <ReviewForm team={team} repo={repo} detected={detected} />
    </div>
  );
}
