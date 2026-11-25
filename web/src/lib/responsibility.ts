import type { FindingSLA } from "./finding-metrics";

export interface ResponsibilityParty {
  id: number;
  kind: "person" | "team";
  name: string;
  aliases: string[];
  email: string;
  team_id: number | null;
  lead_id: number | null;
  active: boolean;
  open: number;
}
export interface BusinessLine {
  id: number;
  name: string;
  code: string;
  aliases: string[];
  primary_id: number | null;
  backup_id: number | null;
  team_ids: number[];
  channel_ids: number[];
  sla: FindingSLA | null;
  rules: { key?: string; kind: "domain" | "cidr"; value: string }[];
  active: boolean;
  open: number;
}
export interface ResponsibilityDirectory {
  business_lines: BusinessLine[];
  parties: ResponsibilityParty[];
  fallback_id: number;
  sla: FindingSLA;
  unassigned: number;
  ownerless: number;
  conflicts: number;
}
export interface ResponsibilitySave<T> {
  result: T;
  metrics_updated: boolean;
}
export interface ResponsibilityRuleResult {
  assigned: number;
  unmatched: number;
  conflicts: number;
}
export function defaultResponsibilityOwner(directory: ResponsibilityDirectory, line?: BusinessLine) {
  return [line?.primary_id, line?.backup_id, directory.fallback_id]
    .map((id) => directory.parties.find((p) => p.id === id && p.active))
    .find(Boolean);
}
export function responsibilityLink(page: "findings" | "remediation", line = "", owner = "") {
  const q = new URLSearchParams();
  if (line) q.set("business_line", line);
  if (owner) q.set("assignee", owner);
  if (page === "findings") q.set("active_only", "true");
  return `/function/${page}?${q}`;
}
