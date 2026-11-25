"use client";

import { useEffect, useRef, useState } from "react";

import Link from "next/link";

import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { type FindingPrimaryAsset, orgAPI, orgFindingAssetAPI } from "@/lib/org";
import { refreshOrganizationStatistics } from "@/lib/org-statistics";
import type { Finding } from "@/lib/types";
import { radixOverlayWasOpenAtPointerDown } from "@/lib/utils";

export function FindingAttributionCard({ finding, onSaved }: { finding: Finding; onSaved: () => void }) {
  const attribution = finding.org_attribution;
  const [open, setOpen] = useState(false);
  const [discard, setDiscard] = useState(false);
  const [data, setData] = useState<FindingPrimaryAsset>();
  const [selected, setSelected] = useState("");
  const [canEdit, setCanEdit] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const requestKey = useRef("");
  const continueButton = useRef<HTMLButtonElement>(null);
  const primaryTrigger = useRef<HTMLButtonElement>(null);
  const drawerTrigger = useRef<HTMLButtonElement>(null);
  const dirty = !!data && selected !== String(data.asset_id ?? "");

  useEffect(() => {
    if (open && data && !loading) primaryTrigger.current?.focus();
  }, [open, data, loading]);

  async function openDrawer() {
    setOpen(true);
    setLoading(true);
    setError("");
    setData(undefined);
    requestKey.current = crypto.randomUUID();
    try {
      const [asset, directory] = await Promise.all([
        orgFindingAssetAPI.get(finding.finding_id ?? finding.id),
        orgAPI.directory(),
      ]);
      setData(asset);
      setSelected(String(asset.asset_id ?? ""));
      setCanEdit(directory.can_edit && !finding.inherited);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }

  function changeOpen(value: boolean) {
    if (value) {
      void openDrawer();
      return;
    }
    if (saving || discard) return;
    if (dirty) setDiscard(true);
    else setOpen(false);
  }

  async function save() {
    setSaving(true);
    setError("");
    try {
      await orgFindingAssetAPI.save(finding.finding_id ?? finding.id, Number(selected), requestKey.current);
      setOpen(false);
      onSaved();
      toast.success("归属主资产已保存，后续事件使用新的归属", { duration: 5000 });
      refreshOrganizationStatistics();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm">修复归属</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4 text-sm">
        <div className="grid gap-2 sm:grid-cols-2">
          <p>
            部门：{attribution?.department_name ? attribution.department_name : "部门未归属"}
            {attribution?.department_active === false && "（已停用）"}
          </p>
          <p>
            业务线：{attribution?.business_line_name ? attribution.business_line_name : "业务线未归属"}
            {attribution?.business_line_active === false && "（已停用）"}
          </p>
          <p>
            负责人：{attribution?.owner_name ? attribution.owner_name : "负责人未指派"}
            {attribution?.owner_active === false && "（已停用）"}
          </p>
          <p className="break-all">主资产：{attribution?.asset_identifier ? attribution.asset_identifier : "待确认"}</p>
        </div>
        {(!attribution || attribution.attribution_quality === "unknown") && (
          <p className="text-amber-700 text-xs">
            尚无可核实的主资产归属。确认主资产后，后续事件会记录归属；已有历史保持不变。
          </p>
        )}
        <div className="flex flex-wrap gap-2">
          <Sheet open={open} onOpenChange={changeOpen}>
            <SheetTrigger asChild>
              <Button
                ref={drawerTrigger}
                size="sm"
                variant="outline"
                disabled={finding.inherited}
                title={finding.inherited ? "继承的漏洞请在来源任务中编辑" : undefined}
              >
                确认归属主资产
              </Button>
            </SheetTrigger>
            <SheetContent
              className="data-[side=right]:w-full sm:max-w-lg"
              onInteractOutside={(event) => {
                const target = event.detail.originalEvent.target as Element | null;
                if (!target?.closest?.("[data-slot='sheet-overlay']") || discard || radixOverlayWasOpenAtPointerDown())
                  event.preventDefault();
              }}
              onEscapeKeyDown={(event) => {
                if (discard || saving) event.preventDefault();
              }}
            >
              <SheetHeader>
                <SheetTitle>归属主资产</SheetTitle>
                <SheetDescription>
                  保留全部关联资产，仅按主资产计算部门与负责人。此次修改从保存时生效。
                </SheetDescription>
              </SheetHeader>
              <div className="flex-1 overflow-auto px-4">
                {loading ? (
                  <div className="space-y-3">
                    <Skeleton className="h-5 w-40" />
                    <Skeleton className="h-10 w-full" />
                  </div>
                ) : data ? (
                  <div className="space-y-3">
                    <Label htmlFor="finding-primary-asset">主资产</Label>
                    <Select
                      value={selected}
                      onValueChange={(value) => {
                        setSelected(value);
                        requestKey.current = crypto.randomUUID();
                      }}
                      disabled={!canEdit || saving || data.candidates.length === 0}
                    >
                      <SelectTrigger ref={primaryTrigger} id="finding-primary-asset" className="w-full">
                        <SelectValue placeholder="请选择一个关联资产" />
                      </SelectTrigger>
                      <SelectContent>
                        {data.candidates.map((asset) => (
                          <SelectItem key={asset.asset_id} value={String(asset.asset_id)}>
                            {asset.identifier} · #{asset.asset_id}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    {data.candidates.length === 0 && (
                      <p className="text-muted-foreground text-xs">暂无可选关联资产，请先在来源任务中补充资产关联。</p>
                    )}
                    {!canEdit && <p className="text-muted-foreground text-xs">需要管理员权限才能更改主资产。</p>}
                    <p className="text-muted-foreground text-xs">
                      部门与业务线请到资产归属中修改；负责人按业务线、部门和兜底设置依次解析。
                    </p>
                  </div>
                ) : null}
                {error && (
                  <p role="alert" className="mt-3 text-destructive text-sm">
                    {error}
                  </p>
                )}
              </div>
              <SheetFooter className="border-t bg-background">
                <Button onClick={save} disabled={!dirty || !selected || !canEdit || saving}>
                  {saving ? "保存中…" : "保存"}
                </Button>
              </SheetFooter>
            </SheetContent>
          </Sheet>
          <Button size="sm" variant="ghost" asChild>
            <Link
              href={`/settings/org?tab=assets${attribution?.asset_identifier ? `&asset_q=${encodeURIComponent(attribution.asset_identifier)}&asset_status=all` : ""}`}
            >
              查看资产归属
            </Link>
          </Button>
        </div>
        <Dialog open={discard} onOpenChange={setDiscard}>
          <DialogContent
            onOpenAutoFocus={(event) => {
              event.preventDefault();
              continueButton.current?.focus();
            }}
            onCloseAutoFocus={(event) => {
              event.preventDefault();
              if (open) primaryTrigger.current?.focus();
              else drawerTrigger.current?.focus();
            }}
          >
            <DialogHeader>
              <DialogTitle>放弃修改？</DialogTitle>
              <DialogDescription>尚未保存的主资产选择会丢失。</DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button ref={continueButton} variant="outline" onClick={() => setDiscard(false)}>
                继续编辑
              </Button>
              <Button
                variant="destructive"
                onClick={() => {
                  setDiscard(false);
                  setOpen(false);
                }}
              >
                放弃
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  );
}
