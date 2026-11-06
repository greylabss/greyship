import Link from "next/link";
import { buttonClass } from "@/components/ui/button";
import { SystemPage } from "@/components/ui/system-page";

export default function NotFound() {
  return (
    <SystemPage
      code="404"
      title="This page doesn't exist."
      actions={
        <Link href="/continue" className={buttonClass("secondary")}>
          Back to projects
        </Link>
      }
    />
  );
}
