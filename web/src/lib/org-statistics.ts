import { toast } from "sonner";

import { api } from "./api";
import { GOVERNANCE_MOCK } from "./finding-metrics";

// Shared by organization edits and batch assignment completion. Refresh only
// today's mutable snapshot; stored report payloads are never recalculated.
export function refreshOrganizationStatistics() {
  if (GOVERNANCE_MOCK) return;
  const id = toast.loading("统计将在重算完成后更新", { id: "org-statistics-refresh" });
  void api
    .refreshFindingMetrics()
    .then(() => {
      toast.success("今日统计已更新，历史快照与已保存报告保留", { id, duration: 4000 });
      window.dispatchEvent(new Event("org-statistics-refreshed"));
    })
    .catch((error: Error) => {
      toast.error(`组织变更已保存，统计更新失败：${error.message}`, {
        id,
        duration: 5000,
        action: { label: "重试", onClick: refreshOrganizationStatistics },
      });
    });
}
