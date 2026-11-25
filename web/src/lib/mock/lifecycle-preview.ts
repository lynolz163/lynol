import type { Finding, FindingRetest } from "@/lib/types";

const enabled =
  process.env.NODE_ENV === "development" &&
  process.env.NEXT_PUBLIC_MOCK === "1" &&
  process.env.NEXT_PUBLIC_LIFECYCLE_PREVIEW === "1";

type PreviewSnapshotRow = Pick<
  Finding,
  "severity" | "status" | "ts" | "dispatched_at" | "developer_fixed_at" | "retest_requested_at" | "verified_fixed_at"
>;

const snapshotJSON = process.env.NEXT_PUBLIC_LIFECYCLE_SNAPSHOT;
export const importedLifecycleFindings: Finding[] =
  enabled && snapshotJSON
    ? (JSON.parse(snapshotJSON) as PreviewSnapshotRow[]).map((row, index) => {
        const number = String(index + 1).padStart(3, "0");
        return {
          ...row,
          id: `lifecycle-snapshot-${number}`,
          name: `【线上脱敏】漏洞 ${number}`,
          vulnclass: "线上脱敏快照",
          summary: "从线上读取的处置状态与时间快照。",
          evidence: "",
        };
      })
    : [];

const discovered = "2026-11-24T09:00:00+08:00";
const feedback = {
  dispatched_at: "2026-11-24T09:30:00+08:00",
  developer_fixed_at: "2026-11-24T10:45:00+08:00",
};

export const lifecyclePreviewFindings: Finding[] = enabled
  ? [
      {
        id: "lifecycle-new",
        name: "【验收样例】待下发",
        vulnclass: "处置流程模拟",
        severity: "medium",
        status: "pending",
        ts: discovered,
        summary: "从这条记录开始，可以依次记录下发、开发反馈和模拟复测。",
        evidence: "",
      },
      {
        id: "lifecycle-batch",
        name: "【验收样例】待下发第二条",
        vulnclass: "处置流程模拟",
        severity: "medium",
        status: "pending",
        ts: discovered,
        summary: "可与另一条待下发样例一起选择，验证批量记录时间一致。",
        evidence: "",
      },
      ...["A", "B"].map<Finding>((label) => ({
        id: `lifecycle-awaiting-feedback-${label.toLowerCase()}`,
        name: `【验收样例】待开发反馈 ${label}`,
        vulnclass: "处置流程模拟",
        severity: "medium",
        status: "in_progress",
        ts: discovered,
        dispatched_at: feedback.dispatched_at,
        summary: "已下发，可与其他记录一起批量反馈已修复。",
        evidence: "",
      })),
      {
        id: "lifecycle-feedback",
        name: "【验收样例】开发已反馈修复",
        vulnclass: "处置流程模拟",
        severity: "medium",
        status: "in_progress",
        ts: discovered,
        ...feedback,
        summary: "已下发，开发已反馈修复。发起模拟复测后，可查看验证确认时间。",
        evidence: "",
      },
      {
        id: "lifecycle-fixed",
        name: "【验收样例】验证已通过",
        vulnclass: "处置流程模拟",
        severity: "medium",
        status: "fixed",
        ts: discovered,
        ...feedback,
        summary: "完整处置记录。开发反馈时间早于复测完成后的验证确认时间。",
        evidence: "",
      },
      {
        id: "lifecycle-reproduced",
        name: "【验收样例】复测仍可复现",
        vulnclass: "处置流程模拟",
        severity: "medium",
        status: "in_progress",
        ts: discovered,
        ...feedback,
        summary: "首次模拟复测通过，之后又确认仍可复现。旧结论保留在复测历史里。",
        evidence: "",
      },
    ]
  : [];

type PreviewRetest = Pick<FindingRetest, "created_at" | "finished_at" | "verdict"> & { finding: string };

export const lifecyclePreviewRetests: PreviewRetest[] = enabled
  ? [
      {
        finding: "lifecycle-fixed",
        created_at: "2026-11-24T11:00:00+08:00",
        finished_at: "2026-11-24T11:15:00+08:00",
        verdict: "fixed",
      },
      {
        finding: "lifecycle-reproduced",
        created_at: "2026-11-24T11:00:00+08:00",
        finished_at: "2026-11-24T11:15:00+08:00",
        verdict: "fixed",
      },
      {
        finding: "lifecycle-reproduced",
        created_at: "2026-11-24T11:30:00+08:00",
        finished_at: "2026-11-24T11:45:00+08:00",
        verdict: "reproduced",
      },
    ]
  : [];
