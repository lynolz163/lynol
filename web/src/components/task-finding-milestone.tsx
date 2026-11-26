"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import { ClipboardCheckIcon, WrenchIcon } from "lucide-react";
import { toast } from "sonner";

import { FindingMilestoneDialog } from "@/components/finding-milestone-dialog";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import type { Task, TaskFindingDeveloperFixedPreview, TaskFindingDispatchPreview } from "@/lib/types";

export function TaskFindingMilestone({
  task,
  onUpdated,
  milestone,
}: {
  task: Pick<Task, "id" | "name" | "description" | "status">;
  onUpdated: () => void;
  milestone: "dispatch" | "developer_fixed";
}) {
  const developerFixed = milestone === "developer_fixed";
  const milestoneLabel = developerFixed ? "开发反馈修复" : "下发";
  const [open, setOpen] = useState(false);
  const [previewInput, setPreviewInput] = useState<{ recorded_at?: string }>({});
  const [preview, setPreview] = useState<TaskFindingDispatchPreview | TaskFindingDeveloperFixedPreview | null>(null);
  const [error, setError] = useState("");
  const trigger = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    let active = true;
    const timer = setTimeout(() => {
      const request = developerFixed
        ? api.previewTaskFindingDeveloperFixed(task.id, previewInput.recorded_at)
        : api.previewTaskFindingDispatch(task.id, previewInput.recorded_at);
      void request
        .then((result) => {
          if (active) setPreview(result);
        })
        .catch((cause: Error) => {
          if (active) setError(cause.message);
        });
    }, 300);
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [open, previewInput, task.id, developerFixed]);

  const changeTime = useCallback((value?: string) => {
    setPreviewInput({ recorded_at: value });
    setPreview(null);
    setError("");
  }, []);

  function changeOpen(next: boolean) {
    setOpen(next);
    if (next) changeTime(undefined);
    else requestAnimationFrame(() => trigger.current?.focus());
  }

  let alreadyRecorded = 0;
  if (preview) {
    alreadyRecorded = "already_dispatched" in preview ? preview.already_dispatched : preview.already_recorded;
  }
  const canRecord = preview && ("can_dispatch" in preview ? preview.can_dispatch : preview.can_record);

  return (
    <>
      <Button
        ref={trigger}
        size="sm"
        variant="outline"
        disabled={task.status !== "done"}
        title={task.status === "done" ? `批量记录此任务漏洞的${milestoneLabel}时间` : "任务完成后可批量记录处置时间"}
        onClick={() => changeOpen(true)}
      >
        {developerFixed ? <WrenchIcon /> : <ClipboardCheckIcon />}
        {developerFixed ? "批量反馈修复" : "批量记录下发"}
      </Button>
      <FindingMilestoneDialog
        open={open}
        onOpenChange={changeOpen}
        title={`批量记录${milestoneLabel}时间`}
        description={
          developerFixed
            ? "仅记录本任务漏洞的开发反馈修复时间，不关闭漏洞、不发起复测或发送通知。关联任务的只读漏洞不参与。"
            : "仅记录本任务漏洞的下发时间，不发送通知。关联任务的只读漏洞不参与。"
        }
        disabled={!canRecord || Boolean(error)}
        confirmLabel={`记录 ${preview?.eligible ?? 0} 个漏洞${milestoneLabel}时间`}
        onRecordedAtChange={changeTime}
        onSave={async (at) => {
          const result = developerFixed
            ? await api.recordTaskFindingDeveloperFixed(task.id, at)
            : await api.recordTaskFindingDispatch(task.id, at);
          if (result.recorded) {
            toast.success(`已记录 ${result.recorded} 个漏洞${milestoneLabel}时间`, {
              description: result.skipped ? `跳过 ${result.skipped} 个已有本轮记录或无需处理的漏洞。` : undefined,
            });
          } else {
            toast.info(`本任务没有需要记录${milestoneLabel}时间的漏洞`);
          }
          onUpdated();
        }}
      >
        <div className="space-y-3 rounded-md border bg-muted/20 p-3">
          <p className="break-words font-medium text-sm">
            {preview?.task_name || task.name || task.description}
            <span className="ml-2 font-mono text-muted-foreground text-xs">#{task.id}</span>
          </p>
          <div aria-live="polite" className="space-y-2 text-sm">
            {error ? (
              <div className="space-y-2">
                <p role="alert" className="text-destructive">
                  {error}
                </p>
                <Button size="sm" type="button" variant="outline" onClick={() => changeTime(previewInput.recorded_at)}>
                  重新核对
                </Button>
              </div>
            ) : preview ? (
              <>
                <dl className="grid grid-cols-2 gap-3">
                  <div>
                    <dt className="text-muted-foreground">{developerFixed ? "本次待记录反馈" : "本次待下发"}</dt>
                    <dd className="font-semibold text-xl tabular-nums">{preview.eligible}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">跳过</dt>
                    <dd className="font-semibold text-xl tabular-nums">{alreadyRecorded + preview.skipped_status}</dd>
                  </div>
                </dl>
                {(alreadyRecorded > 0 || preview.skipped_status > 0) && (
                  <p className="text-muted-foreground text-xs">
                    本轮已有{milestoneLabel}记录 {alreadyRecorded} 个 · 已关闭、误报等无需处理 {preview.skipped_status}{" "}
                    个
                  </p>
                )}
                {preview.reason && (
                  <p className={preview.invalid_time ? "text-destructive" : "text-muted-foreground"}>
                    {preview.reason}
                  </p>
                )}
              </>
            ) : (
              <p className="text-muted-foreground">正在核对本任务漏洞…</p>
            )}
          </div>
        </div>
      </FindingMilestoneDialog>
    </>
  );
}
