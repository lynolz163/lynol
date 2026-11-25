"use client";

import { useEffect, useRef, useState } from "react";

import {
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  Filter,
  Info,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  X,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import {
  type OrgAPIError,
  type OrgAsset,
  type OrgAssetPage,
  type OrgAssetSelection,
  type OrgDirectory,
  type OrgRule,
  type OrgRuleApplication,
  type OrgRulePreview,
  type OrgScope,
  orgAPI,
  orgAssetAPI,
} from "@/lib/org";
import { radixOverlayWasOpenAtPointerDown } from "@/lib/utils";

import { AssetOwnershipHistory, OrgAssignment } from "./assignment";
import { SearchChoice } from "./form";

const typeLabels: Record<string, string> = {
  root_domain: "根域名",
  subdomain: "子域名",
  ip: "IP",
  app: "应用",
  service: "服务",
  endpoint: "接口",
};
const sourceLabels: Record<string, string> = { manual: "手动划归", rule: "规则归属", import: "导入归属", "": "未归属" };
const tableClass =
  "w-full text-left text-sm [&_th]:whitespace-nowrap [&_th]:px-4 [&_th]:py-3 [&_th]:font-medium [&_th]:text-muted-foreground [&_td]:px-4 [&_td]:py-3";
interface AssetFilter {
  status: string;
  type: string;
  q: string;
  from: string;
  to: string;
  page: number;
}
const initialFilter: AssetFilter = { status: "unassigned", type: "", q: "", from: "", to: "", page: 1 };
const newRule = (): OrgRule => ({
  id: 0,
  revision: 0,
  match_type: "domain",
  match_value: "",
  department_id: 0,
  business_line_id: null,
  priority: 0,
  active: true,
});

export function OrgAssets({ directory, onChanged }: { directory: OrgDirectory; onChanged: () => Promise<unknown> }) {
  const [ready, setReady] = useState(false);
  const [view, setView] = useState<"assets" | "rules">("assets");
  const [filter, setFilter] = useState<AssetFilter>(initialFilter);
  const [query, setQuery] = useState("");
  const [page, setPage] = useState<OrgAssetPage>();
  const [rules, setRules] = useState<OrgRule[]>([]);
  const [lineFilter, setLineFilter] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [selectedIDs, setSelectedIDs] = useState<number[]>([]);
  const [allSelected, setAllSelected] = useState<number | null>(null);
  const [syncing, setSyncing] = useState(false);
  const [draft, setDraft] = useState<OrgRule | null>(null);
  const [original, setOriginal] = useState<OrgRule | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [saveError, setSaveError] = useState("");
  const [saving, setSaving] = useState(false);
  const [preview, setPreview] = useState<OrgRulePreview | null>(null);
  const [previewing, setPreviewing] = useState(false);
  const [previewError, setPreviewError] = useState("");
  const [discard, setDiscard] = useState(false);
  const [disablePreview, setDisablePreview] = useState<OrgRulePreview | null>(null);
  const [assetDetail, setAssetDetail] = useState<OrgAsset | null>(null);
  const [applyOpen, setApplyOpen] = useState(false);
  const [includeOwned, setIncludeOwned] = useState(false);
  const [scope, setScope] = useState<OrgScope>("future_findings");
  const [application, setApplication] = useState<OrgRuleApplication | null>(null);
  const [applyBusy, setApplyBusy] = useState(false);
  const [applyError, setApplyError] = useState("");
  const [applyKey, setApplyKey] = useState("");
  const [highlight, setHighlight] = useState<number | null>(null);
  const drawerOpener = useRef<HTMLElement | null>(null);
  const detailOpener = useRef<HTMLElement | null>(null);
  const ruleForm = useRef<HTMLDivElement>(null);
  const continueButton = useRef<HTMLButtonElement>(null);
  const applyConfirmButton = useRef<HTMLButtonElement>(null);
  const previewSequence = useRef(0);
  const highlightTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const canEdit = directory.can_edit;
  const dirty = !!draft && JSON.stringify(draft) !== JSON.stringify(original);
  const departmentName = (id: number | null) => directory.departments.find((d) => d.id === id)?.name || "未归属部门";
  const lineName = (id: number | null) => directory.business_lines.find((line) => line.id === id)?.name || "仅部门";
  const ownerName = (id: number | null) => directory.people.find((person) => person.id === id)?.name || "未指派";
  const targetName = (rule: { department_id: number | null; business_line_id: number | null }) =>
    `${departmentName(rule.department_id)}${rule.business_line_id ? ` / ${lineName(rule.business_line_id)}` : ""}`;
  const selection: OrgAssetSelection =
    allSelected !== null
      ? {
          mode: "filter",
          filter: { status: filter.status, type: filter.type, q: filter.q, from: filter.from, to: filter.to },
          expected_count: allSelected,
        }
      : { mode: "ids", ids: selectedIDs, expected_count: selectedIDs.length };
  const pageSelected = !!page?.items.length && page.items.every((asset) => selectedIDs.includes(asset.asset_id));

  useEffect(() => {
    const update = () => {
      setRefresh((value) => value + 1);
    };
    window.addEventListener("org-assignment-changed", update);
    window.addEventListener("org-assignment-assets-refresh", update);
    return () => {
      window.removeEventListener("org-assignment-changed", update);
      window.removeEventListener("org-assignment-assets-refresh", update);
    };
  }, []);
  useEffect(() => {
    if (application && !applyBusy) applyConfirmButton.current?.focus();
  }, [application, applyBusy]);

  useEffect(() => {
    const q = new URLSearchParams(window.location.search);
    setView(q.get("view") === "rules" || q.has("rule_line") ? "rules" : "assets");
    setLineFilter(Number(q.get("rule_line")) || null);
    const next = {
      status: q.get("asset_status") || "unassigned",
      type: q.get("asset_type") || "",
      q: q.get("asset_q") || "",
      from: q.get("asset_from") || "",
      to: q.get("asset_to") || "",
      page: Number(q.get("asset_page")) || 1,
    };
    setFilter(next);
    setQuery(next.q);
    setReady(true);
    return () => {
      if (highlightTimer.current) clearTimeout(highlightTimer.current);
    };
  }, []);
  // biome-ignore lint/correctness/useExhaustiveDependencies: refresh is the explicit reload signal after a successful mutation.
  useEffect(() => {
    if (!ready) return;
    let active = true;
    setLoading(true);
    setLoadError("");
    void Promise.all([
      orgAssetAPI.assets({ ...filter, page: String(filter.page), page_size: "50" }),
      orgAssetAPI.rules(),
    ])
      .then(([assets, ruleList]) => {
        if (active) {
          setPage(assets);
          setRules(ruleList.items);
        }
      })
      .catch((e: Error) => {
        if (active) setLoadError(e.message);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [filter, ready, refresh]);

  function updateURL(nextView: "assets" | "rules", nextFilter = filter, nextLine = lineFilter) {
    const url = new URL(window.location.href);
    url.searchParams.set("view", nextView);
    for (const [key, value] of Object.entries(nextFilter)) {
      const urlKey = `asset_${key}`;
      if (value) url.searchParams.set(urlKey, String(value));
      else url.searchParams.delete(urlKey);
    }
    if (nextLine) url.searchParams.set("rule_line", String(nextLine));
    else url.searchParams.delete("rule_line");
    window.history.replaceState(null, "", url);
  }
  function changeFilter(patch: Partial<AssetFilter>) {
    const next = { ...filter, ...patch, page: patch.page || 1 };
    if (!patch.page) {
      setSelectedIDs([]);
      setAllSelected(null);
    }
    setFilter(next);
    updateURL(view, next);
  }
  function changeView(next: "assets" | "rules") {
    setView(next);
    updateURL(next);
  }
  function editRule(rule: OrgRule, trigger: HTMLElement) {
    drawerOpener.current = trigger;
    setDraft({ ...rule });
    setOriginal({ ...rule });
    setFields({});
    setSaveError("");
    setPreview(null);
    setPreviewError("");
    previewSequence.current++;
  }
  function updateDraft(patch: Partial<OrgRule>) {
    setDraft((current) => current && { ...current, ...patch });
    setPreview(null);
    setPreviewError("");
    setPreviewing(false);
    setSaveError("");
    previewSequence.current++;
    setFields((errors) => {
      const next = { ...errors };
      for (const key of Object.keys(patch)) delete next[key];
      return next;
    });
  }
  function closeRule() {
    if (saving) return;
    if (dirty) setDiscard(true);
    else setDraft(null);
  }
  function localErrors(rule: OrgRule) {
    const errors: Record<string, string> = {};
    if (!rule.match_value.trim())
      errors.match_value =
        rule.match_type === "domain"
          ? "请填写具体域名或通配域名，例如 *.example.com"
          : "请填写 IP 或 CIDR，例如 192.0.2.0/24";
    if (!rule.department_id) errors.department_id = "请选择目标部门";
    if (!Number.isInteger(rule.priority)) errors.priority = "优先级请填写整数，数值越大越优先";
    return errors;
  }
  async function previewDraft(showFieldErrors = true) {
    if (!draft) return;
    const inputErrors = localErrors(draft);
    if (Object.keys(inputErrors).length) {
      if (showFieldErrors) setFields(inputErrors);
      return;
    }
    const sequence = ++previewSequence.current;
    setPreviewing(true);
    setPreviewError("");
    try {
      const data = await orgAssetAPI.previewRule(draft);
      if (sequence === previewSequence.current) {
        setPreview(data);
        if (showFieldErrors) setFields({});
      }
    } catch (e) {
      if (sequence === previewSequence.current) {
        const error = e as OrgAPIError;
        if (showFieldErrors) {
          setPreviewError(error.message);
          setFields(error.fields);
        } else if (!Object.keys(error.fields).length) setPreviewError(error.message);
      }
    } finally {
      if (sequence === previewSequence.current) setPreviewing(false);
    }
  }
  // biome-ignore lint/correctness/useExhaustiveDependencies: this effect deliberately follows draft values; the timer captures that same render's preview function.
  useEffect(() => {
    if (!draft?.match_value.trim() || !draft.department_id || !Number.isInteger(draft.priority)) return;
    const timer = setTimeout(() => {
      void previewDraft(false);
    }, 450);
    return () => clearTimeout(timer);
  }, [draft]);
  async function persistRule() {
    if (!draft || saving) return;
    setSaving(true);
    setSaveError("");
    try {
      const result = await orgAssetAPI.saveRule(draft, crypto.randomUUID());
      setDraft(null);
      setDisablePreview(null);
      setRefresh((value) => value + 1);
      setHighlight(result.rule.id);
      if (highlightTimer.current) clearTimeout(highlightTimer.current);
      highlightTimer.current = setTimeout(() => setHighlight(null), 4500);
      await onChanged().catch(() => toast.warning("规则已保存，配置列表暂时未刷新"));
      const savedToast = toast.success("规则已保存；存量资产归属保持不变", {
        duration: 8000,
        action: {
          label: "撤销",
          onClick: () => {
            void orgAPI
              .undo(result.operation_id)
              .then(async () => {
                toast.dismiss(savedToast);
                setRefresh((value) => value + 1);
                await onChanged().catch(() => toast.warning("规则已撤销，配置列表暂时未刷新"));
                toast.success("已撤销规则配置；已归属资产保持不变", { duration: 5000 });
              })
              .catch((error: Error) => toast.error(error.message));
          },
        },
      });
    } catch (e) {
      const error = e as OrgAPIError;
      setSaveError(error.message);
      setFields(error.fields);
      requestAnimationFrame(() => {
        const target = ruleForm.current?.querySelector<HTMLElement>(
          `#rule-${Object.keys(error.fields)[0] || "match_value"}`,
        );
        target?.scrollIntoView({ block: "center" });
        target?.focus();
      });
    } finally {
      setSaving(false);
    }
  }
  async function prepareSave() {
    if (!draft || saving) return;
    const errors = localErrors(draft);
    if (Object.keys(errors).length) {
      setFields(errors);
      document.getElementById(`rule-${Object.keys(errors)[0]}`)?.focus();
      return;
    }
    if (original?.id && original.active && !draft.active) {
      setSaving(true);
      try {
        setDisablePreview(await orgAssetAPI.previewRule(original));
      } catch (e) {
        setSaveError((e as Error).message);
      } finally {
        setSaving(false);
      }
    } else await persistRule();
  }
  async function syncAssets() {
    setSyncing(true);
    try {
      const result = await orgAssetAPI.sync();
      setRefresh((value) => value + 1);
      await onChanged().catch(() => toast.warning("操作已完成，组织列表暂时未刷新"));
      toast.success(`已同步 ${result.synced} 个资产，自动归属 ${result.assigned} 个，冲突 ${result.conflicts} 个`, {
        duration: 5000,
      });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSyncing(false);
    }
  }
  async function previewApplication() {
    setApplyBusy(true);
    setApplyError("");
    try {
      setApplication(await orgAssetAPI.previewApplication(includeOwned, scope));
      setApplyKey(crypto.randomUUID());
    } catch (e) {
      setApplyError((e as Error).message);
    } finally {
      setApplyBusy(false);
    }
  }
  async function applyRules() {
    if (!application) return;
    setApplyBusy(true);
    setApplyError("");
    try {
      const result = await orgAssetAPI.applyRules(includeOwned, scope, application.config_revision, applyKey);
      setApplyOpen(false);
      setRefresh((value) => value + 1);
      await onChanged().catch(() => toast.warning("操作已完成，组织列表暂时未刷新"));
      toast.success(`已按规则归属 ${result.assigned} 个资产，保留 ${result.protected} 个手动或导入归属`, {
        duration: 5000,
      });
    } catch (e) {
      setApplyError((e as Error).message);
      setApplication(null);
    } finally {
      setApplyBusy(false);
    }
  }
  function explain(asset: OrgAsset, trigger: HTMLElement) {
    detailOpener.current = trigger;
    setAssetDetail(asset);
  }
  const ruleOptions = directory.business_lines.map((line) => ({
    id: line.id,
    name: line.name,
    detail: departmentName(line.department_id),
  }));
  const visibleRules = rules.filter((rule) => !lineFilter || rule.business_line_id === lineFilter);
  const candidateTable = (asset: OrgAsset) => (
    <div className="space-y-3">
      <p className="rounded-lg bg-muted/40 p-3 text-sm">{asset.match.reason || "没有可用归属规则"}</p>
      {(asset.manual_protected || asset.source === "manual" || asset.source === "import") && (
        <p className="rounded-lg bg-amber-50 p-3 text-amber-800 text-sm dark:bg-amber-950/30 dark:text-amber-300">
          该资产有手动或导入归属记录，规则不会覆盖。当前归属：{targetName(asset)}。
        </p>
      )}
      <div className="overflow-x-auto rounded-lg border">
        <table className={tableClass}>
          <thead className="border-b bg-muted/30">
            <tr>
              <th>命中规则</th>
              <th>目标</th>
              <th>优先级</th>
              <th>结果</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {asset.match.candidates.map((rule) => (
              <tr key={rule.id}>
                <td className="font-mono text-xs">{rule.match_value}</td>
                <td>{targetName(rule)}</td>
                <td>{rule.priority}</td>
                <td>
                  {rule.id === asset.match.rule_id ? (
                    <Badge>胜出</Badge>
                  ) : (
                    <span className="text-muted-foreground text-xs">候选</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {!asset.match.candidates.length && (
          <p className="p-5 text-center text-muted-foreground text-sm">没有命中规则</p>
        )}
      </div>
      <p className="text-muted-foreground text-xs">
        先按优先级，数值越大越优先；同级比较域名或网段精确度。无法确定唯一目标时保留冲突。
      </p>
    </div>
  );
  const previewTable = (items: OrgAsset[]) => (
    <div className="max-h-64 overflow-auto rounded-lg border">
      <table className={tableClass}>
        <thead className="sticky top-0 border-b bg-background">
          <tr>
            <th>资产</th>
            <th>当前归属</th>
            <th>规则匹配结果</th>
          </tr>
        </thead>
        <tbody className="divide-y">
          {items.map((asset) => (
            <tr key={asset.asset_id}>
              <td>
                <span className="block max-w-56 truncate font-mono text-xs" title={asset.identifier}>
                  {asset.identifier}
                </span>
                <span className="text-muted-foreground text-xs">#{asset.asset_id}</span>
              </td>
              <td className="min-w-32 text-xs">
                {targetName(asset)}
                <span className="mt-1 block text-muted-foreground">{sourceLabels[asset.source] || "未归属"}</span>
              </td>
              <td className="min-w-48">
                <p className="text-xs">
                  {asset.match.status === "matched"
                    ? targetName(asset.match)
                    : asset.match.status === "conflict"
                      ? "存在归属冲突"
                      : "无匹配规则"}
                </p>
                <Button
                  variant="link"
                  size="sm"
                  className="h-auto p-0 text-xs"
                  onClick={(event) => explain(asset, event.currentTarget)}
                >
                  查看依据
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {!items.length && <p className="p-6 text-center text-muted-foreground text-sm">暂无匹配资产</p>}
    </div>
  );

  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          {view === "rules" && (
            <Button variant="ghost" size="icon-sm" aria-label="返回资产列表" onClick={() => changeView("assets")}>
              <ArrowLeft className="size-4" />
            </Button>
          )}
          <div>
            <h2 className="font-medium text-lg">{view === "assets" ? "资产归属" : "归属规则"}</h2>
            <p className="mt-1 text-muted-foreground text-xs">
              {view === "assets"
                ? `上次同步：${page?.last_synced_at ? new Date(page.last_synced_at).toLocaleString("zh-CN") : "尚未同步"}`
                : "规则按优先级和匹配精确度决定归属"}
            </p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {view === "assets" ? (
            <>
              <Button variant="outline" onClick={() => changeView("rules")}>
                <Filter className="size-4" />
                归属规则<Badge variant="secondary">{rules.length}</Badge>
              </Button>
              <Button
                disabled={!canEdit || syncing}
                title={!canEdit ? "需要管理员权限" : undefined}
                onClick={() => void syncAssets()}
              >
                <RefreshCw className={`size-4 ${syncing ? "animate-spin" : ""}`} />
                {syncing ? "同步中…" : "同步 ARTEX 资产"}
                {!!page?.unsynced_count && <span className="ml-1">({page.unsynced_count})</span>}
              </Button>
            </>
          ) : (
            <>
              <Button
                variant="outline"
                disabled={!canEdit || !rules.some((rule) => rule.active)}
                title={!canEdit ? "需要管理员权限" : undefined}
                onClick={() => {
                  setApplyOpen(true);
                  setApplication(null);
                  setIncludeOwned(false);
                  setScope("future_findings");
                  setApplyError("");
                }}
              >
                应用到存量资产
              </Button>
              <Button
                disabled={!canEdit}
                title={!canEdit ? "需要管理员权限" : undefined}
                onClick={(event) => {
                  const line = directory.business_lines.find((b) => b.id === lineFilter);
                  editRule(
                    { ...newRule(), department_id: line?.department_id || 0, business_line_id: line?.id || null },
                    event.currentTarget,
                  );
                }}
              >
                <Plus className="size-4" />
                新增规则
              </Button>
            </>
          )}
        </div>
      </header>
      {loadError && (
        <div
          role="alert"
          className="flex items-center justify-between gap-3 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-destructive text-sm"
        >
          <span>{loadError}</span>
          <Button variant="outline" size="sm" onClick={() => setRefresh((value) => value + 1)}>
            重试
          </Button>
        </div>
      )}
      {view === "assets" ? (
        <>
          <form
            className="flex flex-wrap items-end gap-3 rounded-xl border bg-card p-4"
            onSubmit={(event) => {
              event.preventDefault();
              changeFilter({ q: query });
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="asset-status" className="text-xs">
                归属状态
              </Label>
              <select
                id="asset-status"
                className="h-9 rounded-lg border bg-background px-3 text-sm"
                value={filter.status}
                onChange={(event) => changeFilter({ status: event.target.value })}
              >
                <option value="unassigned">仅未归属</option>
                <option value="all">全部资产</option>
                <option value="manual">手动划归</option>
                <option value="rule">规则归属</option>
                <option value="import">导入归属</option>
                <option value="conflict">规则冲突</option>
              </select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="asset-type" className="text-xs">
                资产类型
              </Label>
              <select
                id="asset-type"
                className="h-9 rounded-lg border bg-background px-3 text-sm"
                value={filter.type}
                onChange={(event) => changeFilter({ type: event.target.value })}
              >
                <option value="">全部类型</option>
                {Object.entries(typeLabels).map(([type, label]) => (
                  <option key={type} value={type}>
                    {label}
                  </option>
                ))}
              </select>
            </div>
            <div className="min-w-40 flex-1 space-y-1.5">
              <Label htmlFor="asset-query" className="text-xs">
                关键字
              </Label>
              <Input
                id="asset-query"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="搜索域名或 IP"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="asset-from" className="text-xs">
                首次发现
              </Label>
              <div className="flex items-center gap-2">
                <Input
                  id="asset-from"
                  type="date"
                  value={filter.from}
                  max={filter.to || undefined}
                  onChange={(event) => changeFilter({ from: event.target.value })}
                  className="w-36"
                />
                <span className="text-muted-foreground">—</span>
                <Input
                  aria-label="首次发现结束日期"
                  type="date"
                  value={filter.to}
                  min={filter.from || undefined}
                  onChange={(event) => changeFilter({ to: event.target.value })}
                  className="w-36"
                />
              </div>
            </div>
            <Button type="submit" variant="outline">
              <Search className="size-4" />
              筛选
            </Button>
          </form>
          {selection.expected_count > 0 && (
            <>
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <span>
                  {allSelected !== null
                    ? `已选中当前筛选的全部 ${allSelected} 个资产`
                    : `已勾选 ${selectedIDs.length} 个资产`}
                </span>
                {allSelected === null && (page?.total || 0) > selectedIDs.length && (
                  <Button
                    variant="link"
                    size="sm"
                    className="h-auto p-0"
                    onClick={() => {
                      setAllSelected(page?.total || 0);
                      setSelectedIDs([]);
                    }}
                  >
                    选择全部 {page?.total || 0} 项
                  </Button>
                )}
                {allSelected !== null && <span className="text-muted-foreground text-xs">确认前会再次核对总数</span>}
              </div>
              <OrgAssignment
                directory={directory}
                selection={selection}
                onClear={() => {
                  setSelectedIDs([]);
                  setAllSelected(null);
                }}
              />
            </>
          )}
          <div className="overflow-x-auto rounded-xl border bg-card">
            <table className={tableClass}>
              <thead className="border-b bg-muted/30">
                <tr>
                  <th className="w-10">
                    <Checkbox
                      id="asset-select-page"
                      aria-label="选择当前页资产"
                      title={!canEdit ? "需要管理员权限" : undefined}
                      disabled={!canEdit || loading || !page?.items.length}
                      checked={
                        allSelected !== null || pageSelected
                          ? true
                          : page?.items.some((asset) => selectedIDs.includes(asset.asset_id))
                            ? "indeterminate"
                            : false
                      }
                      onCheckedChange={(checked) => {
                        setAllSelected(null);
                        const ids = page?.items.map((asset) => asset.asset_id) || [];
                        setSelectedIDs((current) =>
                          checked ? [...new Set([...current, ...ids])] : current.filter((id) => !ids.includes(id)),
                        );
                      }}
                    />
                  </th>
                  <th>资产</th>
                  <th>类型</th>
                  <th>当前归属</th>
                  <th>处理人</th>
                  <th>来源</th>
                  <th>首次发现</th>
                  <th>规则与历史</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {page?.items.map((asset) => (
                  <tr key={asset.asset_id} className="hover:bg-muted/40">
                    <td>
                      <Checkbox
                        aria-label={`选择资产 ${asset.identifier}`}
                        title={
                          !canEdit
                            ? "需要管理员权限"
                            : allSelected !== null
                              ? "已选择全部匹配资产，可先清除选择再单独勾选"
                              : undefined
                        }
                        disabled={!canEdit || loading || allSelected !== null}
                        checked={allSelected !== null || selectedIDs.includes(asset.asset_id)}
                        onCheckedChange={(checked) =>
                          setSelectedIDs((current) =>
                            checked ? [...current, asset.asset_id] : current.filter((id) => id !== asset.asset_id),
                          )
                        }
                      />
                    </td>
                    <td className="min-w-52">
                      <span className="block max-w-80 truncate font-mono text-xs" title={asset.identifier}>
                        {asset.identifier}
                      </span>
                      <span className="mt-1 block text-muted-foreground text-xs">#{asset.asset_id}</span>
                    </td>
                    <td className="whitespace-nowrap">{typeLabels[asset.type] || asset.type}</td>
                    <td className="min-w-32">
                      {departmentName(asset.department_id)}
                      {asset.business_line_id && (
                        <span className="mt-1 block text-muted-foreground text-xs">
                          {lineName(asset.business_line_id)}
                        </span>
                      )}
                    </td>
                    <td className="whitespace-nowrap">{ownerName(asset.owner_id)}</td>
                    <td className="whitespace-nowrap">
                      <Badge variant={asset.source ? "secondary" : "outline"}>
                        {sourceLabels[asset.source] || "未归属"}
                      </Badge>
                    </td>
                    <td className="whitespace-nowrap text-muted-foreground text-xs">
                      {new Date(asset.first_seen_at).toLocaleDateString("zh-CN")}
                    </td>
                    <td>
                      <div className="flex items-center">
                        <Button
                          variant="ghost"
                          size="sm"
                          className={asset.match.status === "conflict" ? "text-amber-700" : ""}
                          onClick={(event) => explain(asset, event.currentTarget)}
                        >
                          {asset.match.status === "conflict" ? "查看冲突" : "查看依据"}
                        </Button>
                        <AssetOwnershipHistory asset={asset} directory={directory} />
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {loading && !page ? (
              <div className="space-y-3 p-5" role="status" aria-label="加载资产">
                {[1, 2, 3].map((key) => (
                  <div key={key} className="h-10 animate-pulse rounded bg-muted" />
                ))}
              </div>
            ) : (
              !page?.items.length && (
                <p className="p-12 text-center text-muted-foreground text-sm">
                  没有符合条件的资产{!page?.last_synced_at && "，可先同步 ARTEX 资产"}
                </p>
              )
            )}
          </div>
          <div className="flex flex-wrap items-center justify-between gap-3 text-muted-foreground text-sm">
            <span>
              {loading && <Loader2 className="mr-2 inline size-3.5 animate-spin" />}共 {page?.total || 0} 个资产
            </span>
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="icon-sm"
                aria-label="上一页"
                disabled={loading || filter.page <= 1}
                onClick={() => changeFilter({ page: filter.page - 1 })}
              >
                <ChevronLeft className="size-4" />
              </Button>
              <span>
                第 {filter.page} / {Math.max(1, Math.ceil((page?.total || 0) / 50))} 页
              </span>
              <Button
                variant="outline"
                size="icon-sm"
                aria-label="下一页"
                disabled={loading || filter.page * 50 >= (page?.total || 0)}
                onClick={() => changeFilter({ page: filter.page + 1 })}
              >
                <ChevronRight className="size-4" />
              </Button>
            </div>
          </div>
        </>
      ) : (
        <>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="w-64">
              <SearchChoice
                label="按业务线筛选规则"
                value={lineFilter ? [lineFilter] : []}
                options={ruleOptions}
                placeholder="全部业务线"
                onChange={(ids) => {
                  setLineFilter(ids[0] || null);
                  updateURL("rules", filter, ids[0] || null);
                }}
              />
            </div>
            <span className="flex items-center gap-1.5 text-muted-foreground text-xs">
              <ShieldCheck className="size-4" />
              手动和导入归属不会被规则覆盖
            </span>
          </div>
          <div className="overflow-x-auto rounded-xl border bg-card">
            <table className={tableClass}>
              <thead className="border-b bg-muted/30">
                <tr>
                  <th>匹配规则</th>
                  <th>目标部门 / 业务线</th>
                  <th>
                    优先级
                    <Info className="ml-1 inline size-3" aria-label="数值越大越优先" />
                  </th>
                  <th>状态</th>
                  <th>
                    <span className="sr-only">编辑</span>
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {visibleRules.map((rule) => (
                  <tr
                    key={rule.id}
                    className={`cursor-pointer ${highlight === rule.id ? "bg-emerald-50 dark:bg-emerald-950/20" : "hover:bg-muted/40"}`}
                    tabIndex={0}
                    aria-label={`编辑规则 ${rule.match_value}`}
                    onClick={(event) => editRule(rule, event.currentTarget)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" && event.target === event.currentTarget)
                        editRule(rule, event.currentTarget);
                    }}
                  >
                    <td className="min-w-48">
                      <span className="font-mono text-sm">{rule.match_value}</span>
                      <span className="mt-1 block text-muted-foreground text-xs">
                        {rule.match_type === "domain" ? "域名" : "IP / CIDR"}
                      </span>
                    </td>
                    <td>{targetName(rule)}</td>
                    <td>{rule.priority}</td>
                    <td>
                      <Badge variant={rule.active ? "secondary" : "outline"}>
                        {rule.target_unavailable ? "目标已停用" : rule.active ? "已启用" : "已停用"}
                      </Badge>
                    </td>
                    <td>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`编辑规则 ${rule.match_value}`}
                        disabled={!canEdit}
                        title={!canEdit ? "需要管理员权限" : undefined}
                        onClick={(event) => {
                          event.stopPropagation();
                          editRule(rule, event.currentTarget);
                        }}
                      >
                        <Pencil className="size-3.5" />
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {loading && !rules.length ? (
              <div className="h-32 animate-pulse bg-muted/30" />
            ) : (
              !visibleRules.length && (
                <p className="p-12 text-center text-muted-foreground text-sm">
                  暂无归属规则，新增规则后可预览命中资产。
                </p>
              )
            )}
          </div>
        </>
      )}
      <Sheet
        open={!!draft}
        onOpenChange={(open) => {
          if (!open) closeRule();
        }}
      >
        <SheetContent
          className="w-full gap-0 p-0 sm:max-w-[680px]"
          showCloseButton={false}
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            document.getElementById("rule-match_value")?.focus();
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            drawerOpener.current?.focus();
          }}
          onInteractOutside={(event) => {
            const target = event.detail.originalEvent.target as Element | null;
            if (
              !target?.closest?.("[data-slot='sheet-overlay']") ||
              discard ||
              disablePreview ||
              assetDetail ||
              radixOverlayWasOpenAtPointerDown()
            )
              event.preventDefault();
          }}
        >
          <SheetHeader className="shrink-0 border-b px-6 py-5">
            <div className="flex items-center justify-between">
              <SheetTitle>{draft?.id ? "编辑归属规则" : "新增归属规则"}</SheetTitle>
              <Button variant="ghost" size="icon-sm" aria-label="关闭规则抽屉" disabled={saving} onClick={closeRule}>
                <X className="size-4" />
              </Button>
            </div>
            <SheetDescription>保存后用于后续入库资产；存量资产需单独预览并应用。</SheetDescription>
          </SheetHeader>
          <div ref={ruleForm} className="flex-1 space-y-6 overflow-y-auto px-6 py-6">
            {saveError && (
              <p role="alert" className="rounded-lg bg-destructive/10 p-3 text-destructive text-sm">
                {saveError}
              </p>
            )}
            {draft && (
              <>
                <fieldset disabled={saving || !canEdit} className="space-y-5">
                  <div className="grid gap-4 sm:grid-cols-[140px_1fr]">
                    <div className="space-y-2">
                      <Label htmlFor="rule-match_type">匹配类型</Label>
                      <select
                        id="rule-match_type"
                        className="h-9 w-full rounded-lg border bg-background px-3 text-sm"
                        value={draft.match_type}
                        onChange={(event) => updateDraft({ match_type: event.target.value as OrgRule["match_type"] })}
                      >
                        <option value="domain">域名</option>
                        <option value="ip_cidr">IP / CIDR</option>
                      </select>
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="rule-match_value">
                        匹配值<span className="ml-1 text-destructive">*</span>
                      </Label>
                      <Input
                        id="rule-match_value"
                        value={draft.match_value}
                        placeholder={draft.match_type === "domain" ? "*.example.com" : "192.0.2.0/24"}
                        onChange={(event) => updateDraft({ match_value: event.target.value })}
                        onBlur={() => void previewDraft()}
                        aria-invalid={!!fields.match_value}
                      />
                      {fields.match_value && (
                        <p role="alert" className="text-destructive text-xs">
                          {fields.match_value}
                        </p>
                      )}
                    </div>
                  </div>
                  <div className="grid gap-4 sm:grid-cols-2">
                    <div className="space-y-2">
                      <Label htmlFor="rule-department_id">
                        目标部门<span className="ml-1 text-destructive">*</span>
                      </Label>
                      <SearchChoice
                        id="rule-department_id"
                        label="目标部门"
                        value={draft.department_id ? [draft.department_id] : []}
                        options={directory.departments
                          .filter((d) => d.active || d.id === draft.department_id)
                          .map((d) => ({ id: d.id, name: d.name + (d.active ? "" : "（已停用）") }))}
                        onChange={(ids) => updateDraft({ department_id: ids[0] || 0, business_line_id: null })}
                        disabled={saving || !canEdit}
                        invalid={!!fields.department_id}
                        placeholder="选择目标部门"
                      />
                      {fields.department_id && (
                        <p role="alert" className="text-destructive text-xs">
                          {fields.department_id}
                        </p>
                      )}
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="rule-business_line_id">目标业务线</Label>
                      <SearchChoice
                        id="rule-business_line_id"
                        label="目标业务线"
                        value={draft.business_line_id ? [draft.business_line_id] : []}
                        options={directory.business_lines
                          .filter(
                            (line) =>
                              line.department_id === draft.department_id &&
                              (line.active || line.id === draft.business_line_id),
                          )
                          .map((line) => ({ id: line.id, name: line.name + (line.active ? "" : "（已停用）") }))}
                        onChange={(ids) => updateDraft({ business_line_id: ids[0] || null })}
                        disabled={!draft.department_id || saving || !canEdit}
                        invalid={!!fields.business_line_id}
                        placeholder="仅划归到部门"
                      />
                      {fields.business_line_id && (
                        <p role="alert" className="text-destructive text-xs">
                          {fields.business_line_id}
                        </p>
                      )}
                    </div>
                  </div>
                  <p className="text-muted-foreground text-xs">
                    目标选项来自已保存的部门和业务线配置。
                    <Button
                      type="button"
                      variant="link"
                      size="sm"
                      className="ml-2 h-auto p-0 text-xs"
                      onClick={() => void onChanged().catch((e: Error) => toast.error(e.message))}
                    >
                      刷新选项
                    </Button>
                  </p>
                  <div className="flex items-end justify-between gap-4">
                    <div className="w-40 space-y-2">
                      <Label htmlFor="rule-priority">优先级</Label>
                      <Input
                        id="rule-priority"
                        type="number"
                        step={1}
                        value={draft.priority}
                        onChange={(event) => updateDraft({ priority: Number(event.target.value) })}
                        onBlur={() => void previewDraft()}
                        aria-invalid={!!fields.priority}
                      />
                      {fields.priority && (
                        <p role="alert" className="text-destructive text-xs">
                          {fields.priority}
                        </p>
                      )}
                    </div>
                    <div className="mb-2 flex items-center gap-2">
                      <Label htmlFor="rule-active">启用规则</Label>
                      <Switch
                        id="rule-active"
                        checked={draft.active}
                        onCheckedChange={(active) => updateDraft({ active })}
                      />
                    </div>
                  </div>
                  <p className="text-muted-foreground text-xs">
                    优先级数值越大越优先；同级下具体域名优于通配符，小网段优于大网段。
                  </p>
                </fieldset>
                <section className="space-y-4 border-t pt-5">
                  <div className="flex items-center justify-between">
                    <h3 className="font-medium">命中预览</h3>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={previewing || saving}
                      onClick={() => void previewDraft()}
                    >
                      {previewing ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
                      更新预览
                    </Button>
                  </div>
                  {previewError && (
                    <p role="alert" className="text-destructive text-sm">
                      {previewError}
                    </p>
                  )}
                  {preview ? (
                    <>
                      <p className="text-sm">
                        命中 <strong>{preview.matched}</strong> 个资产，其中 <strong>{preview.unassigned}</strong>{" "}
                        个当前未归属
                      </p>
                      <div className="flex flex-wrap gap-2">
                        <Badge variant={preview.manual_conflicts ? "secondary" : "outline"}>
                          手动 / 导入归属保护 {preview.manual_conflicts}
                        </Badge>
                        <Badge variant={preview.conflicts ? "destructive" : "outline"}>
                          规则冲突 {preview.conflicts}
                        </Badge>
                      </div>
                      {previewTable(preview.items)}
                      {preview.matched > preview.items.length && (
                        <p className="text-muted-foreground text-xs">
                          计数覆盖全部匹配资产，列表展示前 {preview.items.length} 个。
                        </p>
                      )}
                    </>
                  ) : (
                    <div className="rounded-lg border border-dashed p-5 text-muted-foreground text-sm">
                      填写匹配规则与目标部门后，预览命中资产及冲突。
                    </div>
                  )}
                  <p className="flex items-start gap-2 text-muted-foreground text-xs">
                    <ShieldCheck className="size-3.5 shrink-0" />
                    手动或导入的资产归属不会被覆盖；同级规则无法确定唯一目标时保留未归属或原归属。
                  </p>
                </section>
              </>
            )}
          </div>
          <footer className="flex shrink-0 items-center justify-end gap-3 border-t bg-background px-6 py-4 pb-[max(1rem,env(safe-area-inset-bottom))]">
            <Button variant="outline" disabled={saving} onClick={closeRule}>
              取消
            </Button>
            <Button
              disabled={saving || !canEdit || !dirty}
              title={!canEdit ? "需要管理员权限" : undefined}
              onClick={() => void prepareSave()}
            >
              {saving ? "保存中…" : "保存规则"}
            </Button>
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
            if (draft) document.getElementById("rule-match_value")?.focus();
            else drawerOpener.current?.focus();
          }}
        >
          <DialogHeader>
            <DialogTitle>放弃修改？</DialogTitle>
            <DialogDescription>尚未保存的规则内容将丢失。</DialogDescription>
          </DialogHeader>
          <div className="flex justify-end gap-2">
            <Button ref={continueButton} variant="outline" onClick={() => setDiscard(false)}>
              继续编辑
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                setDiscard(false);
                setDraft(null);
              }}
            >
              放弃
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      <Dialog
        open={!!disablePreview}
        onOpenChange={(open) => {
          if (!open && !saving) setDisablePreview(null);
        }}
      >
        <DialogContent
          showCloseButton={!saving}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            document.getElementById("rule-active")?.focus();
          }}
        >
          <DialogHeader>
            <DialogTitle>停用这条归属规则？</DialogTitle>
            <DialogDescription>
              原规则可命中 {disablePreview?.matched || 0} 个资产。停用后不再参与后续匹配，已有归属记录保留。
            </DialogDescription>
          </DialogHeader>
          {saveError && (
            <p role="alert" className="text-destructive text-sm">
              {saveError}
            </p>
          )}
          <div className="flex justify-end gap-2">
            <Button variant="outline" disabled={saving} onClick={() => setDisablePreview(null)}>
              返回编辑
            </Button>
            <Button variant="destructive" disabled={saving} onClick={() => void persistRule()}>
              {saving ? "保存中…" : "停用并保存规则"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      <Dialog
        open={applyOpen}
        onOpenChange={(open) => {
          if (!applyBusy) setApplyOpen(open);
        }}
      >
        <DialogContent className="sm:max-w-3xl" showCloseButton={!applyBusy}>
          <DialogHeader>
            <DialogTitle>将规则应用到存量资产</DialogTitle>
            <DialogDescription>先检查影响，再确认执行。手动和导入归属始终保留。</DialogDescription>
          </DialogHeader>
          <fieldset disabled={applyBusy} className="space-y-4">
            <label className="flex items-start gap-3 rounded-lg border p-3">
              <input
                type="checkbox"
                className="mt-1"
                checked={includeOwned}
                onChange={(event) => {
                  setIncludeOwned(event.target.checked);
                  setApplication(null);
                }}
              />
              <span>
                <span className="block text-sm">同时重新计算已有规则归属的资产</span>
                <span className="mt-1 block text-muted-foreground text-xs">
                  默认只应用到未归属资产。勾选后可能改变原规则归属。
                </span>
              </span>
            </label>
            <div className="grid gap-3 sm:grid-cols-2">
              <label className="flex items-start gap-2 rounded-lg border p-3">
                <input
                  type="radio"
                  name="rule-apply-scope"
                  className="mt-1"
                  checked={scope === "future_findings"}
                  onChange={() => {
                    setScope("future_findings");
                    setApplication(null);
                  }}
                />
                <span>
                  <span className="block text-sm">仅对新漏洞生效</span>
                  <span className="mt-1 block text-muted-foreground text-xs">已发现漏洞继续沿用原归属。</span>
                </span>
              </label>
              <label className="flex items-start gap-2 rounded-lg border p-3">
                <input
                  type="radio"
                  name="rule-apply-scope"
                  className="mt-1"
                  checked={scope === "all_open"}
                  onChange={() => {
                    setScope("all_open");
                    setApplication(null);
                  }}
                />
                <span>
                  <span className="block text-sm">同时更新存量待处置漏洞</span>
                  <span className="mt-1 block text-muted-foreground text-xs">历史事件归属不变。</span>
                </span>
              </label>
            </div>
          </fieldset>
          {application && (
            <div className="space-y-3">
              <div className="grid grid-cols-3 gap-2 sm:grid-cols-6">
                {[
                  ["将归属", application.assigned],
                  ["保持原状", application.unchanged],
                  ["受保护", application.protected],
                  ["冲突", application.conflicts],
                  ["未命中", application.unmatched],
                  ["影响漏洞", application.affected_open],
                ].map(([label, count]) => (
                  <div key={label} className="rounded-lg bg-muted/40 p-3">
                    <p className="text-muted-foreground text-xs">{label}</p>
                    <p className="mt-1 font-semibold text-xl tabular-nums">{count}</p>
                  </div>
                ))}
              </div>
              {previewTable(application.items)}
              <p className="text-muted-foreground text-xs">
                计数覆盖全部资产，明细最多展示 100 个。冲突资产保留原归属。
              </p>
            </div>
          )}
          {applyError && (
            <p role="alert" className="text-destructive text-sm">
              {applyError}
            </p>
          )}
          <div className="flex justify-end gap-2">
            <Button variant="outline" disabled={applyBusy} onClick={() => setApplyOpen(false)}>
              取消
            </Button>
            <Button
              variant={application ? "outline" : "default"}
              disabled={applyBusy}
              onClick={() => void previewApplication()}
            >
              {applyBusy ? "处理中…" : application ? "重新预览" : "预览影响"}
            </Button>
            {application && (
              <Button
                ref={applyConfirmButton}
                disabled={applyBusy || application.assigned === 0}
                onClick={() => void applyRules()}
              >
                确认归属 {application.assigned} 个资产
              </Button>
            )}
          </div>
        </DialogContent>
      </Dialog>
      <Dialog
        open={!!assetDetail}
        onOpenChange={(open) => {
          if (!open) setAssetDetail(null);
        }}
      >
        <DialogContent
          className="sm:max-w-2xl"
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            detailOpener.current?.focus();
          }}
        >
          <DialogHeader>
            <DialogTitle>规则匹配依据</DialogTitle>
            <DialogDescription className="break-all">{assetDetail?.identifier}</DialogDescription>
          </DialogHeader>
          {assetDetail && candidateTable(assetDetail)}
        </DialogContent>
      </Dialog>
    </div>
  );
}
