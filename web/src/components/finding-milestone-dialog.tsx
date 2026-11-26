"use client";

import { type FormEvent, type ReactNode, useEffect, useId, useState } from "react";

import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

function currentBeijingTime() {
  return new Date(Date.now() + 8 * 3600_000).toISOString().slice(0, 19);
}

export function FindingMilestoneDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  confirmLabel = "确认记录",
  disabled = false,
  onRecordedAtChange,
  onSave,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  children?: ReactNode;
  confirmLabel?: string;
  disabled?: boolean;
  onRecordedAtChange?: (recordedAt?: string) => void;
  onSave: (recordedAt?: string) => Promise<void>;
}) {
  const [value, setValue] = useState("");
  const [useCurrentTime, setUseCurrentTime] = useState(true);
  const [saving, setSaving] = useState(false);
  const inputID = useId();
  useEffect(() => {
    if (open) {
      setValue(currentBeijingTime());
      setUseCurrentTime(true);
    }
  }, [open]);

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (saving || disabled) return;
    setSaving(true);
    try {
      await onSave(useCurrentTime ? undefined : new Date(`${value}+08:00`).toISOString());
      onOpenChange(false);
    } catch (error) {
      toast.error(`记录失败：${(error as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !saving && onOpenChange(next)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <form onSubmit={save} className="space-y-4">
          {children}
          <div className="space-y-2">
            <label htmlFor={inputID} className="font-medium text-sm">
              记录时间（北京时间）
            </label>
            <div className="flex flex-wrap gap-2">
              <Input
                id={inputID}
                type="datetime-local"
                step="1"
                required
                max={currentBeijingTime()}
                value={value}
                disabled={saving}
                onChange={(event) => {
                  setValue(event.target.value);
                  setUseCurrentTime(false);
                  const date = new Date(`${event.target.value}+08:00`);
                  onRecordedAtChange?.(Number.isFinite(date.getTime()) ? date.toISOString() : undefined);
                }}
                className="min-w-56 flex-1"
              />
              <Button
                type="button"
                variant="outline"
                disabled={saving}
                onClick={() => {
                  setValue(currentBeijingTime());
                  setUseCurrentTime(true);
                  onRecordedAtChange?.(undefined);
                }}
              >
                使用当前时间
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">
              {useCurrentTime
                ? "已选择当前时间，确认时自动记录。也可修改日期和时间，手动补录。"
                : "按北京时间精确到秒，保存后使用填写的时间记录流转历史。"}
            </p>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
              取消
            </Button>
            <Button type="submit" disabled={saving || disabled}>
              {saving ? "保存中…" : confirmLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
