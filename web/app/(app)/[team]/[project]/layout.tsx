export function generateStaticParams() {
  return [{ project: "acme-api" }, { project: "telegram-bot" }, { project: "payments-service" }];
}

export default function ProjectLayout({ children }: LayoutProps<"/[team]/[project]">) {
  return children;
}
