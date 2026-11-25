"use client";

import { useEffect } from "react";

import { useRouter } from "next/navigation";

export default function ResponsibilityPage() {
  const router = useRouter();
  useEffect(() => {
    router.replace(`/settings/org${window.location.search}${window.location.hash}`);
  }, [router]);

  return (
    <p role="status" className="p-6 text-muted-foreground text-sm">
      正在打开部门与归属…
    </p>
  );
}
