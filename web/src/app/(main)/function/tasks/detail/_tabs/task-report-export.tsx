"use client";

import { useId, useState } from "react";

import { DownloadIcon, MoreHorizontalIcon, Settings2Icon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api } from "@/lib/api";
import type { Task, TaskReportExportOptions, TaskReportTemplate } from "@/lib/types";

type ReportInformation = Omit<TaskReportExportOptions, "template">;

export function TaskReportExport({ task }: { task: Task }) {
  const [template, setTemplate] = useState<TaskReportTemplate>("haixing-html");
  const [information, setInformation] = useState<ReportInformation>({});
  const [draft, setDraft] = useState<ReportInformation>({});
  const [open, setOpen] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [error, setError] = useState("");
  const id = useId();

  async function download(markdown = false) {
    if (downloading) return;
    setDownloading(true);
    setError("");
    try {
      if (markdown) {
        await api.exportFindings({ format: "md-single", scope: "filtered", filters: { task: task.id } });
      } else {
        await api.exportTaskReport(task.id, { template, ...information });
      }
      toast.success("报告已导出");
    } catch (cause) {
      setError(
        cause instanceof Error && cause.name === "TimeoutError"
          ? "导出超过 2 分钟，请稍后重试。已填写的报告信息仍然保留。"
          : `导出失败：${(cause as Error).message}`,
      );
    } finally {
      setDownloading(false);
    }
  }

  return (
    <div className="flex max-w-full flex-wrap items-center gap-2">
      <Select
        value={template}
        onValueChange={(value) => setTemplate(value as TaskReportTemplate)}
        disabled={downloading}
      >
        <SelectTrigger size="sm" aria-label="报告模板">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="haixing-html">网页 HTML</SelectItem>
          <SelectItem value="ola-docx">Word 文档</SelectItem>
        </SelectContent>
      </Select>
      <Button size="sm" onClick={() => download()} disabled={downloading} aria-busy={downloading}>
        <DownloadIcon /> {downloading ? "导出中…" : "导出报告"}
      </Button>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (next) setDraft({ ...information });
          setOpen(next);
        }}
      >
        <DialogTrigger asChild>
          <Button size="sm" variant="outline" disabled={downloading}>
            <Settings2Icon /> 报告信息
          </Button>
        </DialogTrigger>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>报告信息</DialogTitle>
            <DialogDescription>仅用于当前页面的导出；全部可选，不修改任务。</DialogDescription>
          </DialogHeader>
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault();
              setInformation({
                title: draft.title?.trim() || undefined,
                organization: draft.organization?.trim() || undefined,
                author: draft.author?.trim() || undefined,
                report_date: draft.report_date || undefined,
                include_excluded: draft.include_excluded ?? false,
              });
              setOpen(false);
            }}
          >
            <div className="space-y-2">
              <label htmlFor={`${id}-title`} className="font-medium">
                报告标题
              </label>
              <Input
                id={`${id}-title`}
                value={draft.title ?? ""}
                placeholder={task.name?.trim() ? task.name : "留空使用任务名称"}
                onChange={(event) => setDraft({ ...draft, title: event.target.value })}
              />
            </div>
            <div className="space-y-2">
              <label htmlFor={`${id}-organization`} className="font-medium">
                编制单位
              </label>
              <Input
                id={`${id}-organization`}
                value={draft.organization ?? ""}
                placeholder="选填"
                onChange={(event) => setDraft({ ...draft, organization: event.target.value })}
              />
            </div>
            <div className="space-y-2">
              <label htmlFor={`${id}-author`} className="font-medium">
                编制人
              </label>
              <Input
                id={`${id}-author`}
                value={draft.author ?? ""}
                placeholder="选填"
                onChange={(event) => setDraft({ ...draft, author: event.target.value })}
              />
            </div>
            <div className="space-y-2">
              <label htmlFor={`${id}-date`} className="font-medium">
                报告日期
              </label>
              <Input
                id={`${id}-date`}
                type="date"
                value={draft.report_date ?? ""}
                onChange={(event) => setDraft({ ...draft, report_date: event.target.value })}
                aria-describedby={`${id}-date-help`}
              />
              <p id={`${id}-date-help`} className="text-muted-foreground text-xs">
                留空使用导出当天。
              </p>
            </div>
            <div className="flex items-start gap-2">
              <Checkbox
                id={`${id}-excluded`}
                checked={draft.include_excluded ?? false}
                onCheckedChange={(checked) => setDraft({ ...draft, include_excluded: checked === true })}
              />
              <label htmlFor={`${id}-excluded`} className="text-sm leading-4">
                包含误报、重复和已忽略记录
              </label>
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                取消
              </Button>
              <Button type="submit">应用</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="icon-sm" variant="outline" disabled={downloading} aria-label="更多导出格式">
            <MoreHorizontalIcon />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onSelect={() => download(true)}>
            <DownloadIcon /> 下载 Markdown
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {error && (
        <p role="alert" className="w-full max-w-xl text-destructive text-xs">
          {error}
        </p>
      )}
    </div>
  );
}
