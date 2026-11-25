"use client";

import { useEffect, useState } from "react";

import Link from "next/link";

import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api } from "@/lib/api";
import { defaultResponsibilityOwner, type ResponsibilityDirectory } from "@/lib/responsibility";

export function FindingResponsibilityDialog({
  ids,
  initial,
  onSaved,
  disabled = false,
  label = "分配归属",
}: {
  ids: string[];
  initial?: { business_line?: string; assignee?: string };
  onSaved: (businessLine: string, assignee: string, updated: string[]) => void;
  disabled?: boolean;
  label?: string;
}) {
  const [open, setOpen] = useState(false);
  const [businessLine, setBusinessLine] = useState("");
  const [assignee, setAssignee] = useState("");
  const [saving, setSaving] = useState(false);
  const [directory, setDirectory] = useState<ResponsibilityDirectory>();
  useEffect(() => {
    if (open) api.responsibilityDirectory().then(setDirectory, () => toast.error("读取归属配置失败"));
  }, [open]);
  async function save() {
    setSaving(true);
    try {
      const result = await api.setFindingResponsibility(ids, businessLine, assignee);
      onSaved(businessLine, assignee, result.updated);
      toast.success(`已更新 ${result.updated.length} 个漏洞的归属`);
      setOpen(false);
    } catch (error) {
      toast.error((error as Error).message);
    } finally {
      setSaving(false);
    }
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        setOpen(value);
        if (value) {
          setBusinessLine(initial?.business_line ?? "");
          setAssignee(initial?.assignee ?? "");
        }
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm" variant="outline" disabled={disabled ? true : ids.length === 0}>
          {label}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>分配业务线与负责人</DialogTitle>
          <DialogDescription>更新所选 {ids.length} 个漏洞，归属调整会保留在流转历史中。</DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          <Label>业务线</Label>
          <Select
            value={businessLine || "__none__"}
            onValueChange={(value) => {
              const name = value === "__none__" ? "" : value;
              setBusinessLine(name);
              if (directory)
                setAssignee(
                  defaultResponsibilityOwner(
                    directory,
                    directory.business_lines.find((b) => b.name === name),
                  )?.name ?? "",
                );
            }}
          >
            <SelectTrigger aria-label="分配业务线" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__none__">业务线未归属</SelectItem>
              {directory?.business_lines
                .filter((b) => b.active || b.name === businessLine)
                .map((b) => (
                  <SelectItem key={b.id} value={b.name}>
                    {b.name}
                    {!b.active && "（已停用）"}
                  </SelectItem>
                ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-2">
          <Label>负责人 / 团队</Label>
          <Select
            value={assignee || "__none__"}
            onValueChange={(value) => setAssignee(value === "__none__" ? "" : value)}
          >
            <SelectTrigger aria-label="分配负责人" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__none__">负责人未指派</SelectItem>
              {directory?.parties
                .filter((p) => p.active || p.name === assignee)
                .map((p) => (
                  <SelectItem key={p.id} value={p.name}>
                    {p.name}
                    {p.kind === "team" ? " · 团队" : ""}
                    {!p.active && "（已停用）"}
                  </SelectItem>
                ))}
            </SelectContent>
          </Select>
        </div>
        <div className="flex items-center justify-between pt-3">
          <Link href="/system/responsibility" className="text-primary text-xs hover:underline">
            管理业务线与人员 ↗
          </Link>
          <Button onClick={() => void save()} disabled={saving || !directory}>
            {saving ? "保存中…" : "保存归属"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
