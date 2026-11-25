"use client";

import { useEffect, useRef, useState } from "react";

import { AlertTriangle, ArrowRight, CheckCircle2, Clock3, History, Loader2, Plus, X } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import {
  type OrgAPIError,
  type OrgAsset,
  type OrgAssetAudit,
  type OrgAssetSelection,
  type OrgAssignmentJob,
  type OrgAssignmentPreview,
  type OrgDirectory,
  type OrgOwnershipSnapshot,
  type OrgRule,
  type OrgScope,
  orgAssetAPI,
  orgAssignmentAPI,
} from "@/lib/org";
import { radixOverlayWasOpenAtPointerDown } from "@/lib/utils";

import { SearchChoice } from "./form";

const observedJobs = new Set<string>();
const severityNames = { critical: "严重", high: "高危", medium: "中危", low: "低危", unknown: "未定级" };
const jobNames = { queued: "等待执行", running: "划归中", succeeded: "已完成", failed: "执行失败" };
const changeEvent = "org-assignment-changed";

function completedAssignment(operation: string, assigned: number, jobID: string) {
  if (jobID) observedJobs.add(jobID);
  let undoing = false;
  const undoKey = crypto.randomUUID();
  const toastID = toast.success(`已划归 ${assigned} 个资产`, {
    duration: 8000,
    description: "8 秒内可撤销；有新事件的资产会追加反向划归，历史记录保留。",
    action: {
      label: "撤销",
      onClick: () => {
        if (undoing) return;
        undoing = true;
        void orgAssignmentAPI
          .undo(operation, undoKey)
          .then((result) => {
            toast.dismiss(toastID);
            toast.success(
              result.mode === "restore"
                ? `已恢复 ${result.restored} 个资产的原归属`
                : result.mode === "reverse"
                  ? `已反向划归 ${result.reversed} 个资产，保留期间产生的事件`
                  : `已恢复 ${result.restored} 个资产，另 ${result.reversed} 个已反向划归`,
              { duration: 5000 },
            );
            window.dispatchEvent(new Event(changeEvent));
          })
          .catch((error: Error) => {
            undoing = false;
            toast.error(error.message, { duration: 5000 });
          });
      },
    },
  });
  if (jobID) void orgAssignmentAPI.acknowledge(jobID).catch(() => undefined);
}

export function OrgAssignmentJobs({ onChanged }: { onChanged: () => Promise<unknown> }) {
  const [jobs, setJobs] = useState<OrgAssignmentJob[]>([]);
  const [error, setError] = useState("");
  const [expanded, setExpanded] = useState(false);
  const [retry, setRetry] = useState(0);
  const changed = useRef(onChanged);
  changed.current = onChanged;

  // biome-ignore lint/correctness/useExhaustiveDependencies: retry is an explicit reload signal; callback uses the latest ref.
  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let running = false;
    async function loadJobs() {
      if (running) return;
      running = true;
      if (timer) clearTimeout(timer);
      try {
        const result = await orgAssignmentAPI.jobs();
        if (!active) return;
        setJobs(result.items);
        setError("");
        let finished = false;
        for (const job of result.items) {
          if (job.acknowledged || observedJobs.has(job.id)) continue;
          if (job.state === "succeeded") {
            completedAssignment(job.operation_id, job.assigned, job.id);
            finished = true;
          } else if (job.state === "failed") {
            observedJobs.add(job.id);
            toast.error(`资产划归失败：${job.error_detail || "请查看任务记录"}`, { duration: 5000 });
            void orgAssignmentAPI.acknowledge(job.id).catch(() => undefined);
            finished = true;
          }
        }
        if (finished) {
          void changed.current().catch(() => undefined);
          window.dispatchEvent(new Event("org-assignment-assets-refresh"));
        }
        if (result.items.some((job) => job.state === "queued" || job.state === "running")) {
          timer = setTimeout(() => void loadJobs(), 4000);
        }
      } catch (e) {
        if (active) setError((e as Error).message);
      } finally {
        running = false;
      }
    }
    const update = () => {
      void changed.current().catch(() => undefined);
      setRetry((value) => value + 1);
    };
    void loadJobs();
    window.addEventListener(changeEvent, update);
    return () => {
      active = false;
      if (timer) clearTimeout(timer);
      window.removeEventListener(changeEvent, update);
    };
  }, [retry]);

  const activeJobs = jobs.filter((job) => job.state === "queued" || job.state === "running");
  if (!jobs.length && !error) return null;
  return (
    <section className="rounded-xl border bg-card" aria-label="资产划归任务">
      <div className="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
        <div className="flex items-center gap-2 text-sm">
          {activeJobs.length ? (
            <Loader2 className="size-4 animate-spin text-primary" />
          ) : (
            <Clock3 className="size-4 text-muted-foreground" />
          )}
          <span>{activeJobs.length ? `${activeJobs.length} 个资产划归任务正在后台执行` : "资产划归任务"}</span>
          {!!activeJobs.length && <span className="text-muted-foreground text-xs">可离开页面，返回后查看结果</span>}
        </div>
        <Button variant="ghost" size="sm" onClick={() => setExpanded(!expanded)}>
          {expanded ? "收起记录" : `查看记录（${jobs.length}）`}
        </Button>
      </div>
      {error && (
        <div
          role="alert"
          className="flex items-center justify-between gap-2 border-t px-4 py-3 text-destructive text-sm"
        >
          <span>任务状态暂未刷新：{error}</span>
          <Button size="sm" variant="outline" onClick={() => setRetry((n) => n + 1)}>
            重新加载
          </Button>
        </div>
      )}
      {(expanded || !!activeJobs.length) && (
        <div className="divide-y border-t">
          {(expanded ? jobs : activeJobs).map((job) => (
            <div key={job.id} className="space-y-2 px-4 py-3 text-sm">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  <Badge variant={job.state === "failed" ? "destructive" : "secondary"}>{jobNames[job.state]}</Badge>
                  <span>
                    {job.state === "succeeded"
                      ? `已划归 ${job.assigned} 个资产`
                      : `${job.processed} / ${job.total} 个资产`}
                  </span>
                </div>
                <time className="text-muted-foreground text-xs">
                  {new Date(job.created_at).toLocaleString("zh-CN")}
                </time>
              </div>
              {(job.state === "queued" || job.state === "running") && (
                <div
                  className="h-1.5 overflow-hidden rounded-full bg-muted"
                  role="progressbar"
                  aria-label="划归进度"
                  aria-valuemin={0}
                  aria-valuemax={job.total}
                  aria-valuenow={job.processed}
                >
                  <div
                    className="h-full rounded-full bg-primary transition-all"
                    style={{ width: `${job.total ? Math.min(100, (job.processed / job.total) * 100) : 0}%` }}
                  />
                </div>
              )}
              {job.error_detail && <p className="text-destructive text-xs">{job.error_detail}</p>}
              {!!job.undo_mode && (
                <p className="text-muted-foreground text-xs">
                  已撤销 ·{" "}
                  {job.undo_mode === "restore"
                    ? "恢复原归属"
                    : job.undo_mode === "reverse"
                      ? "追加反向划归"
                      : "恢复与反向划归"}
                </p>
              )}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

export function OrgAssignment({
  directory,
  selection,
  onClear,
}: {
  directory: OrgDirectory;
  selection: OrgAssetSelection;
  onClear: () => void;
}) {
  const [department, setDepartment] = useState<number | null>(null);
  const [line, setLine] = useState<number | null>(null);
  const [open, setOpen] = useState(false);
  const [scope, setScope] = useState<OrgScope>("all_open");
  const [backdate, setBackdate] = useState(false);
  const [generateRules, setGenerateRules] = useState(false);
  const [rules, setRules] = useState<{ key: string; rule: OrgRule }[]>([]);
  const [preview, setPreview] = useState<OrgAssignmentPreview | null>(null);
  const [lastPreview, setLastPreview] = useState<OrgAssignmentPreview | null>(null);
  const [overwriteConfirmed, setOverwriteConfirmed] = useState(false);
  const [backdateConfirmed, setBackdateConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [discard, setDiscard] = useState(false);
  const opener = useRef<HTMLElement | null>(null);
  const continueButton = useRef<HTMLButtonElement>(null);
  const previewButton = useRef<HTMLButtonElement>(null);
  const confirmButton = useRef<HTMLButtonElement>(null);
  const form = useRef<HTMLDivElement>(null);
  const errorBox = useRef<HTMLDivElement>(null);
  const key = useRef("");
  const selectionSignature = JSON.stringify(selection);
  const directoryName = (id: number | null) => directory.departments.find((d) => d.id === id)?.name || "未归属部门";
  const lineName = (id: number | null) => directory.business_lines.find((b) => b.id === id)?.name || "仅部门";
  const target = `${directoryName(department)}${line ? ` / ${lineName(line)}` : ""}`;

  // biome-ignore lint/correctness/useExhaustiveDependencies: a changed selection invalidates its read-only preview.
  useEffect(() => {
    setPreview(null);
    setLastPreview(null);
    setBackdate(false);
    setBackdateConfirmed(false);
    setOverwriteConfirmed(false);
    setGenerateRules(false);
    setRules([]);
  }, [selectionSignature]);

  useEffect(() => {
    if (error && !busy) {
      errorBox.current?.scrollIntoView({ block: "nearest" });
      errorBox.current?.focus();
      return;
    }
    if (preview && !busy) {
      const checkbox = form.current?.querySelector<HTMLInputElement>("input[data-confirmation]:not(:checked)");
      if (checkbox) checkbox.focus();
      else confirmButton.current?.focus();
    }
  }, [preview, busy, error]);

  function invalidate() {
    setPreview(null);
    setOverwriteConfirmed(false);
    setBackdateConfirmed(false);
    setError("");
    setFields({});
  }
  function selectDepartment(id: number | null) {
    setDepartment(id);
    setLine(null);
    setRules((rows) =>
      rows.map((row) => ({ ...row, rule: { ...row.rule, department_id: id ?? 0, business_line_id: null } })),
    );
    invalidate();
  }
  function selectLine(id: number | null) {
    setLine(id);
    setRules((rows) => rows.map((row) => ({ ...row, rule: { ...row.rule, business_line_id: id } })));
    invalidate();
  }
  async function loadPreview() {
    if (!department) {
      setFields({ department_id: "请选择接收资产的部门" });
      return;
    }
    setBusy(true);
    setError("");
    setFields({});
    try {
      const result = await orgAssignmentAPI.preview({
        selection,
        department_id: department,
        business_line_id: line,
        scope,
        backdate_first_assignment: backdate,
        generated_rules: generateRules ? rules.map((row) => row.rule) : [],
      });
      setPreview(result);
      setLastPreview(result);
      setOverwriteConfirmed(false);
      setBackdateConfirmed(false);
      key.current = crypto.randomUUID();
    } catch (e) {
      const failure = e as OrgAPIError;
      setError(failure.message);
      setFields(failure.fields || {});
      setPreview(null);
    } finally {
      setBusy(false);
    }
  }
  async function confirmAssignment() {
    if (!preview) return;
    setBusy(true);
    setError("");
    try {
      const result = await orgAssignmentAPI.confirm(preview.token, key.current, overwriteConfirmed, backdateConfirmed);
      if (result.state === "failed") {
        const job = await orgAssignmentAPI.job(result.job_id);
        setError(job.error_detail || "划归未完成，请重新预览后重试");
        setPreview(null);
        return;
      }
      setOpen(false);
      if (result.state === "succeeded") completedAssignment(result.operation_id, result.assigned, result.job_id);
      else
        toast.success(`已提交 ${result.total} 个资产的划归任务`, {
          description: "后台会继续执行，可在任务记录查看进度。",
          duration: 5000,
        });
      window.dispatchEvent(new Event(changeEvent));
      onClear();
    } catch (e) {
      const failure = e as OrgAPIError;
      setError(failure.message);
      setFields(failure.fields || {});
      // A changed preview must be recreated; all user-entered fields remain intact.
      if (failure.code !== "") setPreview(null);
    } finally {
      setBusy(false);
    }
  }
  function close() {
    if (busy) return;
    if (scope !== "all_open" || backdate || generateRules || preview) setDiscard(true);
    else setOpen(false);
  }
  function updateRule(rowKey: string, patch: Partial<OrgRule>) {
    setRules((rows) => rows.map((row) => (row.key === rowKey ? { ...row, rule: { ...row.rule, ...patch } } : row)));
    invalidate();
  }
  async function checkRule(rowKey: string, rule: OrgRule) {
    const fieldKey = `rule-${rowKey}`;
    if (!rule.match_value.trim()) {
      setFields((current) => ({ ...current, [fieldKey]: "请填写域名、IP 或 CIDR，或移除这一行" }));
      return;
    }
    await orgAssetAPI
      .previewRule(rule)
      .then(() => {
        setFields((current) => {
          const next = { ...current };
          delete next[fieldKey];
          return next;
        });
      })
      .catch((e: OrgAPIError) =>
        setFields((current) => ({ ...current, [fieldKey]: e.fields.match_value || e.message })),
      );
  }
  const departmentOptions = directory.departments.filter((d) => d.active).map((d) => ({ id: d.id, name: d.name }));
  const lineOptions = directory.business_lines
    .filter((b) => b.active && b.department_id === department)
    .map((b) => ({ id: b.id, name: b.name }));
  return (
    <>
      <div className="sticky top-0 z-10 flex flex-wrap items-center gap-3 rounded-xl border border-primary/20 bg-background p-4 shadow-sm">
        <div className="mr-auto">
          <p className="font-medium text-sm">已选择 {selection.expected_count} 个资产</p>
          <p className="mt-1 text-muted-foreground text-xs">
            {selection.mode === "filter" ? "包含当前筛选的全部结果" : "按勾选资产划归"}
          </p>
        </div>
        <div className="w-44">
          <SearchChoice
            label="接收部门"
            value={department ? [department] : []}
            options={departmentOptions}
            onChange={(ids) => selectDepartment(ids[0] || null)}
            placeholder="选择部门（必填）"
            invalid={!!fields.department_id}
          />
        </div>
        <div className="w-44">
          <SearchChoice
            label="接收业务线"
            value={line ? [line] : []}
            options={lineOptions}
            onChange={(ids) => selectLine(ids[0] || null)}
            disabled={!department}
            placeholder="业务线（可选）"
          />
        </div>
        <Button
          disabled={!directory.can_edit}
          onClick={(event) => {
            if (!department) {
              setFields({ department_id: "请选择接收资产的部门" });
              return;
            }
            opener.current = event.currentTarget;
            setOpen(true);
            void loadPreview();
          }}
        >
          预览并划归
          <ArrowRight className="size-4" />
        </Button>
        <Button variant="ghost" size="icon-sm" aria-label="清除资产选择" onClick={onClear}>
          <X className="size-4" />
        </Button>
        {fields.department_id && (
          <p role="alert" className="w-full text-destructive text-xs">
            {fields.department_id}
          </p>
        )}
      </div>
      <Sheet
        open={open}
        onOpenChange={(value) => {
          if (!value) close();
        }}
      >
        <SheetContent
          className="w-full gap-0 p-0 sm:max-w-[720px]"
          showCloseButton={false}
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            document.getElementById("assignment-department")?.focus();
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            if (opener.current?.isConnected) opener.current.focus();
            else document.getElementById("asset-select-page")?.focus();
          }}
          onInteractOutside={(event) => {
            const targetElement = event.detail.originalEvent.target as Element | null;
            if (
              !targetElement?.closest?.("[data-slot='sheet-overlay']") ||
              discard ||
              radixOverlayWasOpenAtPointerDown()
            )
              event.preventDefault();
          }}
          onEscapeKeyDown={(event) => {
            if (discard || busy) event.preventDefault();
          }}
        >
          <SheetHeader className="border-b px-6 py-5 pr-14">
            <SheetTitle>批量划归资产</SheetTitle>
            <SheetDescription>预览不修改数据，确认后统一生效并保留归属历史。</SheetDescription>
            <Button
              className="absolute top-4 right-4"
              variant="ghost"
              size="icon-sm"
              aria-label="关闭划归抽屉"
              disabled={busy}
              onClick={close}
            >
              <X className="size-4" />
            </Button>
          </SheetHeader>
          <div ref={form} className="min-h-0 flex-1 space-y-6 overflow-y-auto px-6 py-5">
            <fieldset disabled={busy} className="space-y-5">
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="assignment-department">接收部门 *</Label>
                  <SearchChoice
                    id="assignment-department"
                    label="接收部门"
                    value={department ? [department] : []}
                    options={departmentOptions}
                    onChange={(ids) => selectDepartment(ids[0] || null)}
                    invalid={!!fields.department_id}
                    placeholder="请选择部门"
                  />
                </div>
                <div className="space-y-2">
                  <Label>业务线</Label>
                  <SearchChoice
                    label="接收业务线"
                    value={line ? [line] : []}
                    options={lineOptions}
                    onChange={(ids) => selectLine(ids[0] || null)}
                    disabled={!department}
                    placeholder="不指定，仅划到部门"
                  />
                </div>
              </div>
              <div className="space-y-2">
                <Label>生效范围</Label>
                <div className="grid gap-3 sm:grid-cols-2">
                  <label
                    className={`flex items-start gap-2 rounded-lg border p-3 ${scope === "all_open" ? "border-primary/40 bg-primary/5" : ""}`}
                  >
                    <input
                      type="radio"
                      name="assignment-scope"
                      checked={scope === "all_open"}
                      onChange={() => {
                        setScope("all_open");
                        invalidate();
                      }}
                      className="mt-1"
                    />
                    <span>
                      <span className="block text-sm">存量漏洞随资产一起划归</span>
                      <span className="mt-1 block text-muted-foreground text-xs">
                        更新待处置漏洞的当前归属，历史事件归属保留。
                      </span>
                    </span>
                  </label>
                  <label
                    className={`flex items-start gap-2 rounded-lg border p-3 ${scope === "future_findings" ? "border-primary/40 bg-primary/5" : ""}`}
                  >
                    <input
                      type="radio"
                      name="assignment-scope"
                      checked={scope === "future_findings"}
                      onChange={() => {
                        setScope("future_findings");
                        setBackdate(false);
                        invalidate();
                      }}
                      className="mt-1"
                    />
                    <span>
                      <span className="block text-sm">仅对之后发现的漏洞生效</span>
                      <span className="mt-1 block text-muted-foreground text-xs">已发现漏洞继续沿用原归属。</span>
                    </span>
                  </label>
                </div>
              </div>
              <div className="rounded-lg border p-3">
                <label className="flex items-start gap-2">
                  <input
                    type="checkbox"
                    checked={backdate}
                    disabled={scope === "future_findings" || !lastPreview?.backdate_eligible}
                    onChange={(event) => {
                      setBackdate(event.target.checked);
                      invalidate();
                    }}
                    className="mt-1"
                  />
                  <span>
                    <span className="block text-sm">回溯到资产首次发现时间</span>
                    <span className="mt-1 block text-muted-foreground text-xs">
                      仅限从未归属过的资产。
                      {!lastPreview
                        ? "请先完成预览，确认是否可补录。"
                        : !lastPreview.backdate_eligible
                          ? "本次选择包含已有归属历史的资产，不能回溯。"
                          : "补录会改变这些资产的历史统计，需要再次确认。"}
                    </span>
                  </span>
                </label>
                {backdate && (
                  <p className="mt-3 flex items-start gap-2 text-amber-700 text-xs dark:text-amber-400">
                    <AlertTriangle className="size-4 shrink-0" />
                    此操作会将首次发现以来的归属补录为 {target}，会改变历史统计。
                  </p>
                )}
              </div>
              <section className="space-y-3 rounded-lg border p-3">
                <label className="flex items-start gap-2">
                  <input
                    type="checkbox"
                    checked={generateRules}
                    onChange={(event) => {
                      setGenerateRules(event.target.checked);
                      if (event.target.checked && !rules.length)
                        setRules(
                          (lastPreview?.suggested_rules || []).map((rule) => ({
                            key: crypto.randomUUID(),
                            rule: { ...rule, department_id: department || 0, business_line_id: line },
                          })),
                        );
                      invalidate();
                    }}
                    className="mt-1"
                  />
                  <span>
                    <span className="block text-sm">同时生成归属规则</span>
                    <span className="mt-1 block text-muted-foreground text-xs">
                      最多提供 20 条精确规则建议，不代表覆盖整批资产。可修改后重新预览；手动归属始终优先。
                    </span>
                  </span>
                </label>
                {generateRules && (
                  <div className="space-y-3">
                    {rules.map(({ key: rowKey, rule }) => (
                      <div key={rowKey} className="space-y-1.5">
                        <div className="flex flex-wrap items-center gap-2">
                          <select
                            aria-label="建议规则类型"
                            className="h-9 rounded-lg border bg-background px-2 text-sm"
                            value={rule.match_type}
                            onChange={(event) =>
                              updateRule(rowKey, { match_type: event.target.value as OrgRule["match_type"] })
                            }
                          >
                            <option value="domain">域名</option>
                            <option value="ip_cidr">IP / CIDR</option>
                          </select>
                          <Input
                            aria-label="建议规则匹配值"
                            className="min-w-40 flex-1"
                            value={rule.match_value}
                            onChange={(event) => updateRule(rowKey, { match_value: event.target.value })}
                            onBlur={() => void checkRule(rowKey, rule)}
                            placeholder={rule.match_type === "domain" ? "*.example.com" : "192.0.2.0/24"}
                          />
                          <Input
                            aria-label="建议规则优先级"
                            title="优先级，数值越大越优先"
                            className="w-20"
                            type="number"
                            value={rule.priority}
                            onChange={(event) => updateRule(rowKey, { priority: Number(event.target.value) })}
                          />
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label="移除建议规则"
                            onClick={() => {
                              setRules((rows) => rows.filter((row) => row.key !== rowKey));
                              invalidate();
                            }}
                          >
                            <X className="size-4" />
                          </Button>
                        </div>
                        {fields[`rule-${rowKey}`] && (
                          <p className="text-destructive text-xs">{fields[`rule-${rowKey}`]}</p>
                        )}
                      </div>
                    ))}
                    {!rules.length && (
                      <p className="text-muted-foreground text-xs">
                        暂无可用建议，请添加规则，或取消“同时生成归属规则”。
                      </p>
                    )}
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        const rowKey = crypto.randomUUID();
                        setRules((rows) => [
                          ...rows,
                          {
                            key: rowKey,
                            rule: {
                              id: 0,
                              revision: 0,
                              match_type: "domain",
                              match_value: "",
                              department_id: department || 0,
                              business_line_id: line,
                              priority: 0,
                              active: true,
                            },
                          },
                        ]);
                        invalidate();
                        requestAnimationFrame(() => {
                          const inputs = form.current?.querySelectorAll<HTMLInputElement>(
                            'input[aria-label="建议规则匹配值"]',
                          );
                          inputs?.[inputs.length - 1]?.focus();
                        });
                      }}
                    >
                      <Plus className="size-3.5" />
                      添加规则
                    </Button>
                  </div>
                )}
              </section>
            </fieldset>
            {busy && !preview && (
              <div
                role="status"
                className="flex items-center gap-2 rounded-lg bg-muted/40 p-5 text-muted-foreground text-sm"
              >
                <Loader2 className="size-4 animate-spin" />
                正在核对资产和影响范围…
              </div>
            )}
            {preview ? (
              <section className="space-y-4" aria-label="划归影响预览">
                <div className="flex items-center gap-2">
                  <CheckCircle2 className="size-4 text-emerald-600" />
                  <h3 className="font-medium">影响预览</h3>
                  <Badge variant="outline">尚未写入</Badge>
                </div>
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                  {[
                    ["资产", preview.asset_count],
                    ["待处置漏洞", preview.open_count],
                    ["本次影响漏洞", preview.affected_open],
                    ["规则冲突", preview.rule_conflicts],
                  ].map(([label, value]) => (
                    <div key={label} className="rounded-lg bg-muted/40 p-3">
                      <p className="text-muted-foreground text-xs">{label}</p>
                      <p className="mt-1 font-semibold text-2xl tabular-nums">{value}</p>
                    </div>
                  ))}
                </div>
                <div className="flex flex-wrap gap-2">
                  {Object.entries(severityNames).map(([level, name]) => (
                    <Badge key={level} variant="secondary">
                      {name} {preview.severity[level as keyof typeof severityNames]}
                    </Badge>
                  ))}
                </div>
                {preview.rule_conflicts > 0 && (
                  <p className="rounded-lg bg-amber-50 p-3 text-amber-800 text-sm dark:bg-amber-950/30 dark:text-amber-300">
                    {preview.rule_conflicts} 个资产与规则归属冲突。本次为手动划归，将按 {target}{" "}
                    生效，后续规则不会覆盖。
                  </p>
                )}
                {!!preview.already_assigned && (
                  <div className="space-y-3 rounded-lg border border-amber-300 bg-amber-50/40 p-4 dark:border-amber-800 dark:bg-amber-950/20">
                    <p className="font-medium text-sm">将调整 {preview.already_assigned} 个已有归属的资产</p>
                    <div className="space-y-2 text-sm">
                      {preview.transfers.map((transfer) => (
                        <div
                          key={`${transfer.department_id}:${transfer.business_line_id}`}
                          className="flex flex-wrap items-center gap-2"
                        >
                          <span>
                            {directoryName(transfer.department_id)}
                            {transfer.business_line_id ? ` / ${lineName(transfer.business_line_id)}` : ""}
                          </span>
                          <ArrowRight className="size-3.5 text-muted-foreground" />
                          <span>{target}</span>
                          <Badge variant="outline">{transfer.count} 个</Badge>
                        </div>
                      ))}
                    </div>
                    <label className="flex items-start gap-2 text-sm">
                      <input
                        data-confirmation
                        type="checkbox"
                        checked={overwriteConfirmed}
                        disabled={busy}
                        onChange={(event) => setOverwriteConfirmed(event.target.checked)}
                        className="mt-1"
                      />
                      <span>我已核对以上调整，同意变更这些资产的归属</span>
                    </label>
                  </div>
                )}
                {backdate && (
                  <label className="flex items-start gap-2 rounded-lg border border-amber-300 bg-amber-50/40 p-4 text-sm dark:border-amber-800 dark:bg-amber-950/20">
                    <input
                      data-confirmation
                      type="checkbox"
                      checked={backdateConfirmed}
                      disabled={busy}
                      onChange={(event) => setBackdateConfirmed(event.target.checked)}
                      className="mt-1"
                    />
                    <span>我确认回溯到首次发现时间，并了解这会改变这些资产的历史统计</span>
                  </label>
                )}
                {preview.asset_count > 500 && (
                  <p className="flex items-start gap-2 text-muted-foreground text-sm">
                    <Clock3 className="mt-0.5 size-4 shrink-0" />
                    超过 500 个资产，将在后台统一执行。可离开页面，返回后查看进度和结果。
                  </p>
                )}
              </section>
            ) : (
              !busy && (
                <p className="rounded-lg border border-dashed p-4 text-muted-foreground text-sm">
                  选项已变更，请重新预览后再确认划归。
                </p>
              )
            )}
            {error && (
              <div
                ref={errorBox}
                tabIndex={-1}
                role="alert"
                className="space-y-1 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-destructive text-sm"
              >
                <p>{error}</p>
                {Object.entries(fields)
                  .filter(([field]) => !field.startsWith("rule-"))
                  .map(([field, message]) => (
                    <p key={field}>{message}</p>
                  ))}
              </div>
            )}
          </div>
          <footer className="flex shrink-0 flex-wrap items-center justify-end gap-2 border-t bg-background px-6 py-4 pb-[max(1rem,env(safe-area-inset-bottom))]">
            <Button variant="outline" disabled={busy} onClick={close}>
              取消
            </Button>
            <Button
              ref={previewButton}
              variant={preview ? "outline" : "default"}
              disabled={busy || (generateRules && !rules.length)}
              onClick={() => void loadPreview()}
            >
              {busy ? "处理中…" : "重新预览"}
            </Button>
            {preview && (
              <Button
                ref={confirmButton}
                disabled={
                  busy ||
                  !directory.can_edit ||
                  preview.asset_count === 0 ||
                  (!!preview.already_assigned && !overwriteConfirmed) ||
                  (backdate && !backdateConfirmed)
                }
                onClick={() => void confirmAssignment()}
              >
                {busy ? "划归中…" : `确认划归 ${preview.asset_count} 个资产`}
              </Button>
            )}
          </footer>
        </SheetContent>
      </Sheet>
      <Dialog open={discard} onOpenChange={setDiscard}>
        <DialogContent
          showCloseButton={false}
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            continueButton.current?.focus();
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            if (open) previewButton.current?.focus();
            else opener.current?.focus();
          }}
        >
          <DialogHeader>
            <DialogTitle>放弃修改？</DialogTitle>
            <DialogDescription>划归尚未提交。关闭后不修改任何资产；已选择的资产会保留。</DialogDescription>
          </DialogHeader>
          <div className="flex justify-end gap-2">
            <Button ref={continueButton} variant="outline" onClick={() => setDiscard(false)}>
              继续编辑
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                setDiscard(false);
                setOpen(false);
                setScope("all_open");
                setBackdate(false);
                setGenerateRules(false);
                setRules([]);
                setPreview(null);
              }}
            >
              放弃
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}

export function AssetOwnershipHistory({ asset, directory }: { asset: OrgAsset; directory: OrgDirectory }) {
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<OrgAssetAudit[] | null>(null);
  const [error, setError] = useState("");
  const opener = useRef<HTMLButtonElement>(null);
  const actionNames: Record<string, string> = {
    manual_assignment: "手动划归",
    first_assignment_backdate: "首次归属补录",
    assignment_undo_restore: "撤销并恢复归属",
    assignment_undo_reverse: "撤销并反向划归",
    undo_restore: "撤销并恢复归属",
    undo_reverse: "撤销并反向划归",
    undo: "撤销划归",
    restore: "恢复原归属",
    reverse: "反向划归",
    apply_rule: "按规则归属",
    register: "登记资产",
    sync: "资产同步",
    transfer: "迁移归属",
  };
  const snapshot = (entry: OrgOwnershipSnapshot, index: number) => {
    const department = directory.departments.find((d) => d.id === entry.department_id);
    const line = directory.business_lines.find((d) => d.id === entry.business_line_id);
    const period =
      entry.scope === "future_findings"
        ? "仅之后发现的漏洞"
        : entry.finding_from || entry.finding_to
          ? `发现时间：${entry.finding_from ? new Date(entry.finding_from).toLocaleString("zh-CN") : "不限起点"} → ${entry.finding_to ? new Date(entry.finding_to).toLocaleString("zh-CN") + "（不含）" : "之后"}`
          : "全部发现时间";
    return (
      <div key={`${entry.valid_from}:${entry.finding_from}:${index}`} className="space-y-1 rounded-lg bg-muted/40 p-3">
        <p className="text-sm">
          {department
            ? `${department.name}${department.active ? "" : "（已停用）"}`
            : entry.department_id
              ? `部门 #${entry.department_id}`
              : "未归属部门"}
          {line
            ? ` / ${line.name}${line.active ? "" : "（已停用）"}`
            : entry.business_line_id
              ? ` / 业务线 #${entry.business_line_id}`
              : ""}
        </p>
        <p className="text-muted-foreground text-xs">{period}</p>
        {entry.source && (
          <p className="text-muted-foreground text-xs">
            来源：
            {({ manual: "手动划归", rule: "规则归属", import: "导入归属" } as Record<string, string>)[entry.source] ||
              entry.source}
          </p>
        )}
        {entry.valid_from && (
          <p className="text-muted-foreground text-xs">
            生效：{new Date(entry.valid_from).toLocaleString("zh-CN")}
            {entry.valid_to ? ` → ${new Date(entry.valid_to).toLocaleString("zh-CN")}` : ""}
          </p>
        )}
      </div>
    );
  };
  return (
    <>
      <Button
        ref={opener}
        variant="ghost"
        size="sm"
        aria-label={`${asset.identifier} 的归属历史`}
        onClick={() => {
          setOpen(true);
          setItems(null);
          setError("");
          void orgAssignmentAPI
            .history(asset.asset_id)
            .then((data) => setItems(data.items))
            .catch((e: Error) => setError(e.message));
        }}
      >
        <History className="size-3.5" />
        历史
      </Button>
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent
          className="w-full gap-0 p-0 sm:max-w-[600px]"
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            opener.current?.focus();
          }}
        >
          <SheetHeader className="border-b px-6 py-5 pr-12">
            <SheetTitle>资产归属历史</SheetTitle>
            <SheetDescription className="break-all">
              {asset.identifier} · #{asset.asset_id}
            </SheetDescription>
          </SheetHeader>
          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-6">
            {error ? (
              <p role="alert" className="text-destructive text-sm">
                {error}
              </p>
            ) : !items ? (
              <div className="space-y-3" role="status" aria-label="加载归属历史">
                {[1, 2, 3].map((n) => (
                  <div key={n} className="h-24 animate-pulse rounded-lg bg-muted" />
                ))}
              </div>
            ) : !items.length ? (
              <p className="py-10 text-center text-muted-foreground text-sm">暂无归属变更记录</p>
            ) : (
              items.map((item) => (
                <article key={item.id} className="space-y-3 rounded-xl border p-4">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span className="font-medium text-sm">
                      {item.actor_label} · {actionNames[item.action] || "归属变更"}
                    </span>
                    <time className="text-muted-foreground text-xs">
                      {new Date(item.recorded_at).toLocaleString("zh-CN")}
                    </time>
                  </div>
                  <p className="text-muted-foreground text-xs">
                    生效时间：{new Date(item.effective_at).toLocaleString("zh-CN")}
                  </p>
                  {(item.before.length > 0 || item.after.length > 0) && (
                    <div className="grid gap-3 sm:grid-cols-2">
                      <div className="space-y-2">
                        <h4 className="text-muted-foreground text-xs">变更前</h4>
                        {item.before.length ? (
                          item.before.map(snapshot)
                        ) : (
                          <p className="rounded-lg bg-muted/40 p-3 text-sm">未归属</p>
                        )}
                      </div>
                      <div className="space-y-2">
                        <h4 className="text-muted-foreground text-xs">变更后</h4>
                        {item.after.length ? (
                          item.after.map(snapshot)
                        ) : (
                          <p className="rounded-lg bg-muted/40 p-3 text-sm">未归属</p>
                        )}
                      </div>
                    </div>
                  )}
                </article>
              ))
            )}
          </div>
        </SheetContent>
      </Sheet>
    </>
  );
}
