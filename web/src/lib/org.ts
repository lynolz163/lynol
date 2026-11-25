import type { NotificationChannel, NotificationKind } from "./types";

export type OrgKind = "department" | "business_line" | "person" | "dev_group" | "fallback";
export type OrgScope = "future_findings" | "all_open";
export interface OrgSLA {
  critical: number;
  high: number;
  medium: number;
  low: number;
}
export interface OrgRole {
  kind: string;
  object_id: number;
  name: string;
  open: number;
}
export interface OrgDepartment {
  id: number;
  name: string;
  code: string;
  active: boolean;
  revision: number;
  head_id: number | null;
  open: number;
  channel_ids: number[];
}
export interface OrgLine {
  id: number;
  name: string;
  code: string;
  department_id: number;
  primary_id: number | null;
  backup_id: number | null;
  group_ids: number[];
  active: boolean;
  revision: number;
  open: number;
  sla: OrgSLA | null;
  channel_ids: number[];
}
export interface OrgPerson {
  id: number;
  name: string;
  email: string;
  department_id: number | null;
  dev_group_id: number | null;
  active: boolean;
  revision: number;
  open: number;
  roles: OrgRole[];
}
export interface OrgGroup {
  id: number;
  name: string;
  department_id: number;
  active: boolean;
  revision: number;
  members: number;
}
export interface OrgDirectory {
  departments: OrgDepartment[];
  business_lines: OrgLine[];
  people: OrgPerson[];
  groups: OrgGroup[];
  fallback_id: number | null;
  config_revision: number;
  sla: OrgSLA;
  can_edit: boolean;
  unassigned_open: number;
}
export interface OrgCommand {
  kind: OrgKind;
  action: "save" | "disable" | "migrate";
  id: number;
  revision: number;
  data: Record<string, unknown>;
  scope: OrgScope;
  receiver_id?: number;
  confirm_impact?: boolean;
  idempotency_key: string;
  channel_drafts?: OrgNotificationDraft[];
  shared_channel_confirmed_ids?: number[];
}
export interface OrgNotificationDraft {
  client_key: string;
  id?: number;
  expected_updated_at?: string;
  name: string;
  kind: string;
  enabled: boolean;
  config: Record<string, unknown>;
}
export interface OrgNotificationChannel extends NotificationChannel {
  org_managed: boolean;
  usage: {
    departments: { id: number; name: string }[];
    business_lines: { id: number; name: string }[];
    schedules: number;
    global: boolean;
  };
}
export interface OrgNotificationOptions {
  channels: OrgNotificationChannel[];
  kinds: NotificationKind[];
  test_mode: "live" | "fake";
}
export interface OrgChangeResult {
  operation_id: string;
  entity_id: number;
  effective_at: string;
  affected_open: number;
}
export interface OrgImpact {
  open: number;
  assets: number;
  business_lines: OrgRole[];
  roles: OrgRole[];
}
export interface OrgAudit {
  id: number;
  actor_label: string;
  action: string;
  effective_at: string;
  recorded_at: string;
  old_value: Record<string, unknown>;
  new_value: Record<string, unknown>;
}
export interface OrgCSVPreview {
  rows: { row: number; action: "create" | "update" | "error"; data: OrgPerson; error?: string; warning?: string }[];
  created: number;
  updated: number;
  errors: number;
}

export class OrgAPIError extends Error {
  constructor(
    message: string,
    public fields: Record<string, string> = {},
    public code = "",
  ) {
    super(message);
  }
}

// Organization requests use the real API even while the rest of a local preview uses fixtures.
async function request<T>(path: string, body?: unknown, method?: string): Promise<T> {
  const token = typeof window === "undefined" ? null : localStorage.getItem("artex_token");
  const response = await fetch(`/api/org${path}`, {
    method: method ?? (body === undefined ? "GET" : "POST"),
    headers: {
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(30_000),
  }).catch(() => {
    throw new OrgAPIError("请求未完成，请检查连接后重试");
  });
  const data = await response.json().catch(() => null);
  if (!data) throw new OrgAPIError("服务暂时不可用，请稍后重试");
  if (!response.ok) throw new OrgAPIError(data.error || "操作失败，请稍后重试", data.fields || {}, data.code || "");
  return data as T;
}

export const orgAPI = {
  directory: () => request<OrgDirectory>("/directory"),
  change: (command: OrgCommand) => request<OrgChangeResult>("/change", command),
  impact: (command: OrgCommand) => request<OrgImpact>("/impact", command),
  check: (params: Record<string, string>) =>
    request<{ errors: Record<string, string> }>(`/check?${new URLSearchParams(params)}`),
  history: async (kind: OrgKind, id: number): Promise<{ items: OrgAudit[] }> => {
    type RawAudit = Omit<OrgAudit, "old_value" | "new_value"> & {
      old_value: Record<string, unknown> | null;
      new_value: Record<string, unknown> | null;
    };
    const data = await request<{ items: RawAudit[] }>(`/history?kind=${kind}&id=${id}`);
    // Creation has no previous object. Normalize once at the API boundary.
    return {
      items: data.items.map((item) => ({ ...item, old_value: item.old_value ?? {}, new_value: item.new_value ?? {} })),
    };
  },
  undo: (operation: string) =>
    request<OrgChangeResult>(`/operations/${operation}/undo`, { idempotency_key: crypto.randomUUID() }),
  importPreview: (csv: string, department_id: number | null) =>
    request<OrgCSVPreview>("/people/import-preview", { csv, department_id }),
  importPeople: (csv: string, department_id: number | null, key: string) =>
    request<{ created: number; updated: number; operation_id: string }>("/people/import", {
      csv,
      department_id,
      idempotency_key: key,
    }),
  notificationChannels: () => request<OrgNotificationOptions>("/notification-channels"),
  testNotificationChannel: (draft: OrgNotificationDraft) =>
    request<{ ok: boolean; latency_ms: number; message?: string }>("/notification-channels/test", draft),
};

export interface OrgRule {
  target_unavailable?: boolean;
  id: number;
  revision: number;
  match_type: "domain" | "ip_cidr";
  match_value: string;
  department_id: number;
  business_line_id: number | null;
  priority: number;
  active: boolean;
}
export interface OrgRuleMatch {
  status: "matched" | "conflict" | "unmatched";
  rule_id: number | null;
  department_id: number | null;
  business_line_id: number | null;
  reason: string;
  candidates: OrgRule[];
}
export interface OrgAsset {
  asset_id: number;
  type: string;
  identifier: string;
  first_seen_at: string;
  department_id: number | null;
  business_line_id: number | null;
  owner_id: number | null;
  source: "manual" | "rule" | "import" | "";
  rule_id: number | null;
  ownership_revision: number;
  manual_protected: boolean;
  registered: boolean;
  open: number;
  match: OrgRuleMatch;
}
export interface OrgAssetPage {
  items: OrgAsset[];
  total: number;
  last_synced_at: string | null;
  unsynced_count: number;
}
export interface OrgRulePreview {
  matched: number;
  unassigned: number;
  manual_conflicts: number;
  conflicts: number;
  items: OrgAsset[];
}
export interface OrgRuleApplication {
  affected_open: number;
  config_revision: number;
  assigned: number;
  unchanged: number;
  conflicts: number;
  protected: number;
  unmatched: number;
  items: OrgAsset[];
  operation_id?: string;
}
export const orgAssetAPI = {
  assets: (filter: Record<string, string>) => request<OrgAssetPage>(`/assets?${new URLSearchParams(filter)}`),
  sync: () =>
    request<{ synced: number; assigned: number; conflicts: number; operation_id: string; last_synced_at: string }>(
      "/assets/sync",
      { idempotency_key: crypto.randomUUID() },
    ),
  rules: () => request<{ items: OrgRule[]; config_revision: number }>("/rules"),
  saveRule: (rule: OrgRule, key: string) =>
    request<{ rule: OrgRule; operation_id: string; effective_at: string }>("/rules/save", {
      rule,
      idempotency_key: key,
    }),
  previewRule: (rule: OrgRule) => request<OrgRulePreview>("/rules/preview", { rule }),
  previewApplication: (include_rule_owned: boolean, scope: OrgScope) =>
    request<OrgRuleApplication>("/rules/apply-preview", { include_rule_owned, scope }),
  applyRules: (include_rule_owned: boolean, scope: OrgScope, config_revision: number, key: string) =>
    request<OrgRuleApplication>("/rules/apply", { include_rule_owned, scope, config_revision, idempotency_key: key }),
};

export interface OrgAssignmentFilter {
  status: string;
  type: string;
  q: string;
  from: string;
  to: string;
}
export type OrgAssetSelection =
  | { mode: "ids"; ids: number[]; expected_count: number }
  | { mode: "filter"; filter: OrgAssignmentFilter; expected_count: number };
export interface OrgAssignmentInput {
  selection: OrgAssetSelection;
  department_id: number;
  business_line_id: number | null;
  scope: OrgScope;
  backdate_first_assignment: boolean;
  generated_rules: OrgRule[];
}
export interface OrgAssignmentPreview {
  token: string;
  expires_at: string;
  asset_count: number;
  open_count: number;
  affected_open: number;
  severity: Record<"critical" | "high" | "medium" | "low" | "unknown", number>;
  already_assigned: number;
  transfers: { department_id: number | null; business_line_id: number | null; count: number }[];
  rule_conflicts: number;
  backdate_eligible: boolean;
  suggested_rules: OrgRule[];
}
export type OrgAssignmentState = "queued" | "running" | "succeeded" | "failed";
export interface OrgAssignmentResult {
  operation_id: string;
  job_id: string;
  state: OrgAssignmentState;
  assigned: number;
  processed: number;
  total: number;
  effective_at?: string;
}
export interface OrgAssignmentJob {
  id: string;
  operation_id: string;
  state: OrgAssignmentState;
  total: number;
  processed: number;
  assigned: number;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  effective_at: string | null;
  error_code: string | null;
  error_detail: string;
  acknowledged: boolean;
  undo_mode?: string;
}
type RawAssignmentJob = Omit<OrgAssignmentJob, "error_detail"> & {
  error_detail: string | { message?: string; error?: string } | null;
};
function assignmentJob(raw: RawAssignmentJob): OrgAssignmentJob {
  return {
    ...raw,
    error_detail:
      typeof raw.error_detail === "string"
        ? raw.error_detail
        : raw.error_detail?.message || raw.error_detail?.error || "",
  };
}
export interface OrgOwnershipSnapshot {
  department_id: number | null;
  business_line_id: number | null;
  source?: string;
  scope?: OrgScope;
  valid_from?: string;
  valid_to?: string | null;
  finding_from?: string | null;
  finding_to?: string | null;
}
export interface OrgAssetAudit extends Omit<OrgAudit, "old_value" | "new_value"> {
  before: OrgOwnershipSnapshot[];
  after: OrgOwnershipSnapshot[];
}
export const orgAssignmentAPI = {
  preview: (input: OrgAssignmentInput) => request<OrgAssignmentPreview>("/assignments/preview", input),
  confirm: (token: string, key: string, overwrite_confirmed: boolean, backdate_confirmed: boolean) =>
    request<OrgAssignmentResult>("/assignments/confirm", {
      token,
      idempotency_key: key,
      overwrite_confirmed,
      backdate_confirmed,
    }),
  jobs: async () => {
    const result = await request<{ items: RawAssignmentJob[] }>("/assignments/jobs");
    return { items: result.items.map(assignmentJob) };
  },
  job: async (id: string) => assignmentJob(await request<RawAssignmentJob>(`/assignments/jobs/${id}`)),
  acknowledge: (id: string) => request<{ ok: boolean }>(`/assignments/jobs/${id}/ack`, {}),
  undo: (operation: string, key: string) =>
    request<{ operation_id: string; mode: "restore" | "reverse" | "mixed"; restored: number; reversed: number }>(
      `/assignments/${operation}/undo`,
      { idempotency_key: key },
    ),
  history: async (id: number): Promise<{ items: OrgAssetAudit[] }> => {
    type RawSnapshot = OrgOwnershipSnapshot | OrgOwnershipSnapshot[] | Record<string, unknown> | null;
    type RawAssetAudit = Omit<OrgAudit, "old_value" | "new_value"> & { old_value: RawSnapshot; new_value: RawSnapshot };
    const result = await request<{ items: RawAssetAudit[] }>(`/history?kind=asset&id=${id}`);
    // Asset history contains multiple finding-time cohorts. Normalize objects and arrays once here.
    const snapshots = (value: RawSnapshot): OrgOwnershipSnapshot[] =>
      Array.isArray(value) ? value : value && "department_id" in value ? [value as OrgOwnershipSnapshot] : [];
    return {
      items: result.items.map(({ old_value, new_value, ...item }) => ({
        ...item,
        before: snapshots(old_value),
        after: snapshots(new_value),
      })),
    };
  },
};

export interface FindingPrimaryAsset {
  finding_id: number;
  asset_id: number | null;
  candidates: { asset_id: number; identifier: string; type: string }[];
  needs_confirmation: boolean;
}
export const orgFindingAssetAPI = {
  get: (id: string) => request<FindingPrimaryAsset>(`/findings/${encodeURIComponent(id)}/primary-asset`),
  save: (id: string, asset_id: number, idempotency_key: string) =>
    request<{ operation_id: string; finding_id: number; asset_id: number; effective_at: string }>(
      `/findings/${encodeURIComponent(id)}/primary-asset`,
      { asset_id, idempotency_key },
      "PUT",
    ),
};
