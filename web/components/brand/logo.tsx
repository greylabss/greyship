import Image from "next/image";
import Link from "next/link";
import lockup from "@/public/brand/greyship-lockup.svg";
import mark from "@/public/brand/greyship-mark.svg";

// Traced from public/brand/source/*.png. SVGs skip the image optimiser, so they stay crisp at any height.
export function Logo({ href = "/", wordmark = true, height = 22 }: { href?: string; wordmark?: boolean; height?: number }) {
  const src = wordmark ? lockup : mark;
  return (
    <Link href={href} className="flex shrink-0 items-center rounded-control" aria-label="Greyship home">
      <Image src={src} alt="" priority unoptimized style={{ height, width: "auto" }} />
    </Link>
  );
}
