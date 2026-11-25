import type { FindingSLA } from "../finding-metrics";
import {
  type BusinessLine,
  defaultResponsibilityOwner,
  type ResponsibilityDirectory,
  type ResponsibilityParty,
} from "../responsibility";
import type { Finding } from "../types";
import { mockFindingSLA, recordMockFindingChange, syncMockGovernanceFindings } from "./remediation";

const parties: ResponsibilityParty[] = [
  {
    id: 1,
    kind: "person",
    name: "王明",
    email: "wangming@example.com",
    team_id: 11,
    lead_id: null,
    active: true,
    aliases: ["王明"],
    open: 0,
  },
  {
    id: 2,
    kind: "person",
    name: "李婷",
    email: "liting@example.com",
    team_id: 12,
    lead_id: null,
    active: true,
    aliases: ["李婷"],
    open: 0,
  },
  {
    id: 3,
    kind: "person",
    name: "赵磊",
    email: "zhaolei@example.com",
    team_id: 13,
    lead_id: null,
    active: true,
    aliases: ["赵磊"],
    open: 0,
  },
  ...["研发一组", "平台组", "研发二组"].map(
    (name, i): ResponsibilityParty => ({
      id: i + 11,
      kind: "team",
      name,
      email: "",
      team_id: null,
      lead_id: i + 1,
      active: true,
      aliases: [name],
      open: 0,
    }),
  ),
];
let directory: ResponsibilityDirectory = {
  parties,
  fallback_id: 3,
  sla: mockFindingSLA,
  unassigned: 0,
  ownerless: 0,
  conflicts: 0,
  business_lines: ["核心业务", "数据平台", "基础设施"].map((name, i) => ({
    id: i + 1,
    name,
    code: ["core", "data", "infra"][i],
    aliases: [name],
    primary_id: i + 1,
    backup_id: ((i + 1) % 3) + 1,
    team_ids: [i + 11],
    channel_ids: [9001, 9002],
    sla: null,
    active: true,
    open: 0,
    rules: [{ kind: "domain", value: `*.${["core", "data", "infra"][i]}.example.com` }],
  })),
};
const active = (finding: Finding) => ["pending", "in_progress", "confirmed"].includes(finding.status);
const storageKey = "artex-responsibility-preview-v1";
let loaded = false;
const conflicts = new Set<string>();

export function mockResponsibilityDirectory(findings: Finding[]) {
  if (!loaded && typeof window !== "undefined") {
    loaded = true;
    const raw = localStorage.getItem(storageKey);
    if (raw) {
      try {
        const saved = JSON.parse(raw) as { directory: ResponsibilityDirectory; findings: Finding[] };
        if (
          Array.isArray(saved.directory?.business_lines) &&
          Array.isArray(saved.directory?.parties) &&
          Array.isArray(saved.findings)
        ) {
          directory = saved.directory;
          for (const finding of findings) {
            const previous = saved.findings.find((f) => f.id === finding.id);
            if (previous)
              Object.assign(finding, {
                business_line: previous.business_line,
                assignee: previous.assignee,
                business_line_id: previous.business_line_id,
                assignee_id: previous.assignee_id,
              });
          }
        }
      } catch {
        localStorage.removeItem(storageKey);
      }
    }
    for (const finding of findings) {
      for (const [field, entries] of [
        ["business_line", directory.business_lines],
        ["assignee", directory.parties],
      ] as const) {
        const value = finding[field];
        if (!value || entries.some((p) => p.name === value || p.aliases.includes(value))) continue;
        if (field === "business_line")
          directory.business_lines.push({
            id: Math.max(0, ...directory.business_lines.map((b) => b.id)) + 1,
            name: value,
            aliases: [value],
            code: `legacy-${directory.business_lines.length + 1}`,
            primary_id: null,
            backup_id: null,
            team_ids: [],
            channel_ids: [],
            rules: [],
            sla: null,
            active: true,
            open: 0,
          });
        else
          directory.parties.push({
            id: Math.max(0, ...directory.parties.map((p) => p.id)) + 1,
            name: value,
            aliases: [value],
            kind: /(组|团队)$/.test(value) ? "team" : "person",
            email: "",
            team_id: null,
            lead_id: null,
            active: true,
            open: 0,
          });
      }
    }
  }
  for (const finding of findings) {
    finding.business_line_id = directory.business_lines.find((b) => b.name === finding.business_line)?.id;
    finding.assignee_id = directory.parties.find((p) => p.name === finding.assignee)?.id;
  }
  const open = findings.filter(active);
  directory.sla = mockFindingSLA;
  directory.unassigned = open.filter((f) => !f.business_line).length;
  directory.ownerless = open.filter((f) => !f.assignee).length;
  directory.conflicts = open.filter((f) => conflicts.has(f.id)).length;
  for (const b of directory.business_lines) b.open = open.filter((f) => f.business_line_id === b.id).length;
  for (const p of directory.parties) p.open = open.filter((f) => f.assignee_id === p.id).length;
  syncMockGovernanceFindings(
    findings,
    Object.fromEntries(directory.business_lines.filter((b) => b.sla).map((b) => [b.name, b.sla as FindingSLA])),
  );
  return structuredClone(directory);
}
export function persistMockResponsibility(findings: Finding[]) {
  mockResponsibilityDirectory(findings);
  localStorage.setItem(storageKey, JSON.stringify({ directory, findings }));
}

function networkAddress(value: string): { number: bigint; bits: number } | null {
  if (value.includes(".")) {
    const parts = value.split(".");
    if (parts.length !== 4 || parts.some((p) => !/^\d{1,3}$/.test(p) || Number(p) > 255)) return null;
    return { number: parts.reduce((n, p) => (n << BigInt(8)) | BigInt(p), BigInt(0)), bits: 32 };
  }
  if (!value.includes(":")) return null;
  const halves = value.split("::");
  if (halves.length > 2) return null;
  const left = halves[0] ? halves[0].split(":") : [],
    right = halves[1] ? halves[1].split(":") : [];
  const count = left.length + right.length;
  if (count > 8 || (halves.length === 1 && count !== 8) || [...left, ...right].some((p) => !/^[0-9a-f]{1,4}$/i.test(p)))
    return null;
  const parts = [...left, ...Array(8 - count).fill("0"), ...right] as string[];
  return { number: parts.reduce((n, p) => (n << BigInt(16)) | BigInt(`0x${p}`), BigInt(0)), bits: 128 };
}

export function saveMockResponsibility(kind: string, body: unknown, findings: Finding[]) {
  mockResponsibilityDirectory(findings);
  let result: unknown;
  if (kind === "business-lines") {
    const line = structuredClone(body as BusinessLine);
    line.name = line.name.trim();
    line.code = line.code.trim();
    if (!line.name || !/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(line.code))
      throw new Error("请填写有效的名称和唯一标识");
    if (
      directory.business_lines.some((b) => b.id !== line.id && (b.aliases.includes(line.name) || b.code === line.code))
    )
      throw new Error("名称或标识已被使用");
    if (line.sla && Object.values(line.sla).some((days) => !Number.isInteger(days) || days < 1 || days > 365))
      throw new Error("SLA 为 1 至 365 天的整数");
    for (const rule of line.rules) {
      rule.value = rule.value.trim().toLowerCase();
      const [address, prefix] = rule.value.split("/");
      const network = rule.kind === "cidr" ? networkAddress(address) : null;
      const invalid =
        rule.kind === "domain"
          ? !/^(\*\.)?([a-z0-9-]+\.)+[a-z0-9-]+$/.test(rule.value)
          : !network || !/^\d+$/.test(prefix ?? "") || Number(prefix) > network.bits;
      if (invalid) throw new Error(`资产规则格式错误：${rule.value}`);
    }
    const previous = directory.business_lines.find((b) => b.id === line.id);
    if (!line.id) line.id = Math.max(0, ...directory.business_lines.map((b) => b.id)) + 1;
    line.aliases = [...new Set([...(previous?.aliases ?? []), line.name])];
    directory.business_lines = [...directory.business_lines.filter((b) => b.id !== line.id), line].sort(
      (a, b) => a.id - b.id,
    );
    if (previous)
      for (const finding of findings.filter((f) => f.business_line === previous.name)) {
        const before = { ...finding };
        finding.business_line = line.name;
        recordMockFindingChange(before, finding);
      }
    result = line;
  } else if (kind === "parties") {
    const party = structuredClone(body as ResponsibilityParty);
    party.name = party.name.trim();
    if (!party.name || directory.parties.some((p) => p.id !== party.id && p.aliases.includes(party.name)))
      throw new Error("名称为空或已被使用");
    if (party.email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(party.email)) throw new Error("邮箱格式不正确");
    if (!party.active && party.id === directory.fallback_id) throw new Error("请先替换兜底负责人，再停用此人员");
    if (!party.active && party.kind === "team" && directory.parties.some((p) => p.active && p.team_id === party.id))
      throw new Error("请先转移团队中的人员");
    const previous = directory.parties.find((p) => p.id === party.id);
    if (!party.id) party.id = Math.max(0, ...directory.parties.map((p) => p.id)) + 1;
    party.aliases = [...new Set([...(previous?.aliases ?? []), party.name])];
    directory.parties = [...directory.parties.filter((p) => p.id !== party.id), party];
    let transferred = 0;
    if (previous)
      for (const finding of findings.filter((f) => f.assignee === previous.name)) {
        const before = { ...finding };
        finding.assignee = party.name;
        if (!party.active && active(finding)) {
          finding.assignee =
            defaultResponsibilityOwner(
              directory,
              directory.business_lines.find((b) => b.id === finding.business_line_id),
            )?.name ?? "";
          transferred++;
        }
        recordMockFindingChange(before, finding);
      }
    result = { party, transferred };
  } else if (kind === "fallback") {
    directory.fallback_id = (body as { id: number }).id;
    for (const finding of findings.filter((f) => !f.assignee && active(f))) {
      const before = { ...finding };
      finding.assignee = defaultResponsibilityOwner(directory)?.name ?? "";
      recordMockFindingChange(before, finding);
    }
    result = body;
  } else if (kind === "apply-rules") {
    const counts = { assigned: 0, unmatched: 0, conflicts: 0 };
    for (const finding of findings.filter((f) => !f.business_line && active(f))) {
      const matches = directory.business_lines.filter(
        (line) =>
          line.active &&
          line.rules.some((rule) =>
            finding.assets?.some((asset) => {
              const host = asset.label
                .replace(/^https?:\/\//, "")
                .split(/[/:]/)[0]
                .toLowerCase();
              if (rule.kind === "domain")
                return rule.value.startsWith("*.") ? host.endsWith(rule.value.slice(1)) : host === rule.value;
              const [network, bits] = rule.value.split("/");
              const actual = networkAddress(asset.label.replace(/^\[|\]$/g, "")),
                base = networkAddress(network);
              if (!actual || !base || actual.bits !== base.bits) return false;
              const shift = BigInt(base.bits - Number(bits));
              return actual.number >> shift === base.number >> shift;
            }),
          ),
      );
      if (matches.length === 1) {
        const before = { ...finding };
        finding.business_line = matches[0].name;
        finding.assignee = defaultResponsibilityOwner(directory, matches[0])?.name ?? "";
        recordMockFindingChange(before, finding);
        counts.assigned++;
        conflicts.delete(finding.id);
      } else if (matches.length > 1) {
        counts.conflicts++;
        conflicts.add(finding.id);
      } else {
        counts.unmatched++;
        conflicts.delete(finding.id);
      }
    }
    result = counts;
  } else throw new Error("未知归属配置");
  persistMockResponsibility(findings);
  return { result, metrics_updated: true };
}
