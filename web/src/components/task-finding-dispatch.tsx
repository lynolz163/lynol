"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import { ClipboardCheckIcon } from "lucide-react";
import { toast } from "sonner";

import { FindingMilestoneDialog } from "@/components/finding-milestone-dialog";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import type { Task, TaskFindingDispatchPreview } from "@/lib/types";

export function TaskFindingDispatch({
  task,
  onUpdated,
  label = "批量记录下发",
}: {
  task: Pick<Task, "id" | "name" | "description" | "status">;
  onUpdated: () => void;
  label?: string;
}) {
  const [open, setOpen] = useState(false);
  const [previewInput, setPreviewInput] = useState<{ recorded_at?: string }>({});
  const [preview, setPreview] = useState<TaskFindingDispatchPreview | null>(null);
  const [error, setError] = useState("");
  const trigger = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    let active = true;
    const timer = setTimeout(() => {
      void api
        .previewTaskFindingDispatch(task.id, previewInput.recorded_at)
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
  }, [open, previewInput, task.id]);

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

  return (
    <>
      <Button
        ref={trigger}
        size="sm"
        variant="outline"
        disabled={task.status !== "done"}
        title={task.status === "done" ? "批量记录此任务漏洞的下发时间" : "任务完成后可批量记录下发时间"}
        onClick={() => changeOpen(true)}
      >
        <ClipboardCheckIcon />
        {label}
      </Button>
      <FindingMilestoneDialog
        open={open}
        onOpenChange={changeOpen}
        title="批量记录下发时间"
        description="仅记录本任务漏洞的下发时间，不发送通知。报告可下载后自行发送。"
        disabled={!preview?.can_dispatch || Boolean(error)}
        confirmLabel={`记录 ${preview?.eligible ?? 0} 个漏洞下发时间`}
        onRecordedAtChange={changeTime}
        onSave={async (at) => {
          const result = await api.recordTaskFindingDispatch(task.id, at);
          if (result.recorded) {
            toast.success(`已记录 ${result.recorded} 个漏洞下发时间`, {
              description: result.skipped ? `跳过 ${result.skipped} 个已有记录或无需下发的漏洞。` : undefined,
            });
          } else {
            toast.info("本任务没有需要记录下发时间的漏洞");
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
                    <dt className="text-muted-foreground">本次待下发</dt>
                    <dd className="font-semibold text-xl tabular-nums">{preview.eligible}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">跳过</dt>
                    <dd className="font-semibold text-xl tabular-nums">
                      {preview.already_dispatched + preview.skipped_status}
                    </dd>
                  </div>
                </dl>
                {(preview.already_dispatched > 0 || preview.skipped_status > 0) && (
                  <p className="text-muted-foreground text-xs">
                    本轮已有下发记录 {preview.already_dispatched} 个 · 已关闭、误报等无需下发 {preview.skipped_status}{" "}
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
