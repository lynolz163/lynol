import {
  type FindingMetricsFilter,
  type FindingMetricsReport,
  findingMetricDuration,
  findingSeverityLabels,
  findingSLATotal,
  findingStageLabels,
} from "./finding-metrics";
import type { OrgDirectory } from "./org";

type Cell = string | number;
export interface GovernanceSheet {
  name: string;
  rows: Cell[][];
}

export function findingOrganizationName(
  kind: "department" | "business_line" | "assignee",
  id: number | null | undefined,
  fallback: string,
  directory?: OrgDirectory,
) {
  if (id === undefined) return fallback;
  const names = { department: "部门", business_line: "业务线", assignee: "负责人" };
  if (!id) return kind === "assignee" ? "负责人未指派" : `${names[kind]}未归属`;
  const items =
    kind === "department"
      ? directory?.departments
      : kind === "business_line"
        ? directory?.business_lines
        : directory?.people;
  const entity = items?.find((item) => item.id === id);
  if (entity) return `${entity.name}${entity.active ? "" : "（已停用）"}`;
  return fallback || `${names[kind]} #${id}`;
}

export function findingFilterLabel(filter: FindingMetricsFilter, directory?: OrgDirectory) {
  const parts: string[] = [];
  if (filter.severity)
    parts.push(
      `等级：${findingSeverityLabels[filter.severity as keyof typeof findingSeverityLabels] ?? filter.severity}`,
    );
  if (filter.department_id != null)
    parts.push(`部门：${findingOrganizationName("department", filter.department_id, "", directory)}`);
  if (filter.business_line_id != null)
    parts.push(`业务线：${findingOrganizationName("business_line", filter.business_line_id, "", directory)}`);
  else if (filter.business_line)
    parts.push(`业务线：${filter.business_line === "__none__" ? "业务线未归属" : filter.business_line}`);
  if (filter.owner_id != null)
    parts.push(`负责人：${findingOrganizationName("assignee", filter.owner_id, "", directory)}`);
  else if (filter.assignee) parts.push(`负责人：${filter.assignee === "__none__" ? "负责人未指派" : filter.assignee}`);
  return parts.join(" · ") || "全部等级、部门、业务线与负责人";
}

export function governanceSheets(
  report: FindingMetricsReport,
  from: string,
  to: string,
  filter: FindingMetricsFilter,
  previous?: FindingMetricsReport,
  directory?: OrgDirectory,
): GovernanceSheet[] {
  const s = report.summary;
  const percent = (met: number, total: number) => (total ? `${((met / total) * 100).toFixed(1)}%` : "无有效样本");
  const sheets: GovernanceSheet[] = [
    {
      name: "指标总览",
      rows: [
        ["指标", "本期", "上期"],
        ["统计开始", from, ""],
        ["统计结束", to, ""],
        ["筛选范围", findingFilterLabel(filter, directory), ""],
        ["新增漏洞", s.new, previous?.summary.new ?? "—"],
        ["关闭次数", s.closed, previous?.summary.closed ?? "—"],
        ["重开次数", s.reopened, previous?.summary.reopened ?? "—"],
        ["待处置存量", report.stock_known ? s.open : "未知", previous?.stock_known ? previous.summary.open : "未知"],
        [
          "逾期数量",
          report.stock_known ? s.overdue : "未知",
          previous?.stock_known ? previous.summary.overdue : "未知",
        ],
        [
          "SLA 达标率（全量口径）",
          report.stock_known ? percent(s.sla_met, findingSLATotal(s)) : "未知",
          previous?.stock_known ? percent(previous.summary.sla_met, findingSLATotal(previous.summary)) : "未知",
        ],
        [
          "SLA 达标率（已关闭口径）",
          percent(s.sla_met, s.sla_closed),
          previous ? percent(previous.summary.sla_met, previous.summary.sla_closed) : "—",
        ],
        ["SLA 达标次数 / 有效关闭", `${s.sla_met} / ${s.sla_closed}`, ""],
        ["复测不通过率", percent(s.retest_failed, s.retest_failed + s.retest_passed), ""],
        ["复测通过 / 不通过 / 其他", `${s.retest_passed} / ${s.retest_failed} / ${s.retest_other}`, ""],
        [
          "一次修复通过率",
          s.first_retest_total > 0 ? percent(s.first_retest_passed, s.first_retest_total) : "无统计数据",
          "",
        ],
        [
          "首次复测通过 / 首次明确结论",
          report.quality_known || s.first_retest_total > 0
            ? `${s.first_retest_passed} / ${s.first_retest_total}`
            : "无统计数据",
          "",
        ],
        ["平均返工轮次", s.verified_fixes ? (s.rework_rounds / s.verified_fixes).toFixed(2) : "无统计数据", ""],
        [
          "返工轮次 / 复测确认修复周期",
          report.quality_known || s.verified_fixes > 0 ? `${s.rework_rounds} / ${s.verified_fixes}` : "无统计数据",
          "",
        ],
        ["账龄：不足 7 天", report.stock_known ? s.ages[0] : "未知", ""],
        ["账龄：7 至不足 30 天", report.stock_known ? s.ages[1] : "未知", ""],
        ["账龄：30 天及以上", report.stock_known ? s.ages[2] : "未知", ""],
        [
          "汇总时间",
          report.generated_at
            ? new Date(report.generated_at).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai" })
            : "无汇总",
          "",
        ],
      ],
    },
    {
      name: "趋势",
      rows: [
        ["日期", "新增", "关闭次数", "重开次数", "期末待处置", "期末逾期"],
        ...report.trend.map((row) => [
          row.day,
          row.new,
          row.closed,
          row.reopened,
          row.open ?? "未知",
          row.overdue ?? "未知",
        ]),
      ],
    },
    {
      name: "阶段时效",
      rows: [
        ["阶段", "样本量", "平均小时", "P90 小时"],
        ...report.durations.map((row) => [
          findingStageLabels[row.key] ?? row.key,
          row.count,
          row.average_hours ?? "无样本",
          row.p90_hours ?? "无样本",
        ]),
      ],
    },
    {
      name: "部门、业务线与负责人",
      rows: [
        [
          "维度",
          "名称",
          "待处置",
          "逾期",
          "逾期率",
          "期间关闭",
          "SLA 达标率（全量）",
          "达标次数",
          "有效关闭次数",
          "平均闭环小时",
          "P90 小时",
        ],
        ...report.groups.map((row) => [
          { department: "部门", business_line: "业务线", assignee: "负责人" }[row.dimension],
          findingOrganizationName(
            row.dimension,
            report.attribution_version ? row.id : undefined,
            row.name ||
              (row.dimension === "assignee"
                ? "负责人未指派"
                : `${row.dimension === "department" ? "部门" : "业务线"}未归属`),
            directory,
          ),
          report.stock_known ? row.open : "未知",
          report.stock_known ? row.overdue : "未知",
          report.stock_known ? percent(row.overdue, row.open) : "未知",
          row.closed,
          report.stock_known ? percent(row.sla_met, findingSLATotal(row)) : "未知",
          row.sla_met,
          row.sla_closed,
          row.average_hours ?? "无样本",
          row.p90_hours ?? "无样本",
        ]),
      ],
    },
    {
      name: "逾期漏洞",
      rows: [
        ["漏洞 ID", "漏洞名称", "等级", "部门", "业务线", "负责人", "存活天数", "逾期天数"],
        ...report.overdue.map((row) => [
          row.id,
          row.name,
          findingSeverityLabels[row.severity as keyof typeof findingSeverityLabels] ?? row.severity,
          report.attribution_version
            ? findingOrganizationName("department", row.department_id, row.department || "部门未归属", directory)
            : "未记录",
          findingOrganizationName(
            "business_line",
            report.attribution_version ? row.business_line_id : undefined,
            row.business_line || "业务线未归属",
            directory,
          ),
          findingOrganizationName(
            "assignee",
            report.attribution_version ? row.owner_id : undefined,
            row.assignee || "负责人未指派",
            directory,
          ),
          row.age_days,
          row.overdue_days,
        ]),
      ],
    },
    {
      name: "统计口径",
      rows: [
        ["项目", "说明"],
        [
          "修复质量覆盖",
          report.quality_known
            ? "按已记录的完整复测链统计"
            : "已知计数按已记录样本展示；缺失记录不补为 0，不推断未记录的周期",
        ],
        ["时区", "Asia/Shanghai（北京时间），自然日计时"],
        ["历史采集开始", report.history_since ?? "未知"],
        [
          "期末 SLA 规则",
          report.sla
            ? Object.entries(report.sla)
                .map(
                  ([key, value]) => `${findingSeverityLabels[key as keyof typeof findingSeverityLabels]} ${value} 天`,
                )
                .join("；")
            : "无汇总",
        ],
        ["关闭", "进入已修复或已处理状态；重开后再次关闭重新计数。开发反馈修复单独记录。"],
        ["分组归属", "期间事件按发生时的业务线与负责人归属，期末存量按期末归属。变更归属不改写历史日快照。"],
        [
          "SLA 全量",
          "期间按时关闭次数 /（期间有效关闭次数 + 期末逾期未关闭数）。尚未到期的待处置漏洞不纳入分母。存量未知则不计算。",
        ],
        [
          "SLA 已关闭",
          "期间按时关闭次数 / 期间有效关闭次数；重开后再次关闭重新计数。首次发现起计时，历史日快照保留当时规则，缺少归属不会暂停 SLA。",
        ],
        [
          "一次修复通过率",
          "已记录历史中首次明确复测结论的通过数 / 首次明确结论总数，按首次结论时间入期，无结论不占首次。",
        ],
        [
          "返工",
          "本期复测确认修复周期的累计失败轮次 / 本期复测确认修复周期数，包含跨期失败与零返工周期。返工耗时只统计发生返工的周期，从本周期首次失败到确认修复。",
        ],
        ["P90", "合并所选期间所有有效样本，取第 ceil(样本数 × 90%) 个值。"],
        ["缺失数据", "缺少原始状态时间时只保留当前状态基线。未知存量不视为 0。"],
        [
          "闭环时长",
          findingMetricDuration(report.durations.find((row) => row.key === "closure")?.average_hours ?? null),
        ],
      ],
    },
  ];
  return sheets;
}

function xml(value: string) {
  // biome-ignore lint/suspicious/noControlCharactersInRegex: XML 1.0 forbids these control characters in cell text.
  const forbidden = /[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g;
  return value
    .replace(forbidden, "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

// A small OOXML workbook with inline strings. Strings are never Excel formulas.
// Stored ZIP entries keep the export dependency-free and work offline in preview.
export function governanceWorkbook(sheets: GovernanceSheet[]): Uint8Array {
  const enc = new TextEncoder();
  const files: { name: string; data: Uint8Array }[] = [];
  const add = (name: string, body: string) =>
    files.push({ name, data: enc.encode(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>${body}`) });
  add(
    "[Content_Types].xml",
    `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>${sheets.map((_, i) => `<Override PartName="/xl/worksheets/sheet${i + 1}.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`).join("")}</Types>`,
  );
  add(
    "_rels/.rels",
    '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>',
  );
  add(
    "xl/workbook.xml",
    `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>${sheets.map((sheet, i) => `<sheet name="${xml(sheet.name)}" sheetId="${i + 1}" r:id="rId${i + 1}"/>`).join("")}</sheets></workbook>`,
  );
  add(
    "xl/_rels/workbook.xml.rels",
    `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">${sheets.map((_, i) => `<Relationship Id="rId${i + 1}" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet${i + 1}.xml"/>`).join("")}<Relationship Id="styles" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`,
  );
  add(
    "xl/styles.xml",
    '<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><color rgb="FFFFFFFF"/><sz val="11"/><name val="Calibri"/></font></fonts><fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF2563EB"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="1"><border/></borders><cellStyleXfs count="1"><xf/></cellStyleXfs><cellXfs count="2"><xf fontId="0" fillId="0" borderId="0" xfId="0"/><xf fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1"/></cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>',
  );
  for (const [i, sheet] of sheets.entries()) {
    const count = Math.max(...sheet.rows.map((row) => row.length));
    const col = (index: number) => {
      let name = "";
      for (let n = index + 1; n > 0; n = Math.floor((n - 1) / 26))
        name = String.fromCharCode(65 + ((n - 1) % 26)) + name;
      return name;
    };
    add(
      `xl/worksheets/sheet${i + 1}.xml`,
      `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><dimension ref="A1:${col(count - 1)}${sheet.rows.length}"/><sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" state="frozen"/></sheetView></sheetViews><cols><col min="1" max="1" width="28" customWidth="1"/><col min="2" max="${Math.max(2, count)}" width="24" customWidth="1"/></cols><sheetData>${sheet.rows.map((row, r) => `<row r="${r + 1}">${row.map((value, c) => (typeof value === "number" ? `<c r="${col(c)}${r + 1}"${r === 0 ? ' s="1"' : ""}><v>${value}</v></c>` : `<c r="${col(c)}${r + 1}" t="inlineStr"${r === 0 ? ' s="1"' : ""}><is><t xml:space="preserve">${xml(value)}</t></is></c>`)).join("")}</row>`).join("")}</sheetData><autoFilter ref="A1:${col(count - 1)}${sheet.rows.length}"/></worksheet>`,
    );
  }
  const local: Uint8Array[] = [],
    central: Uint8Array[] = [];
  let offset = 0,
    centralSize = 0;
  for (const file of files) {
    const name = enc.encode(file.name),
      data = file.data;
    let crc = 0xffffffff;
    for (const byte of data) {
      crc ^= byte;
      for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
    }
    crc = (crc ^ 0xffffffff) >>> 0;
    const header = new Uint8Array(30 + name.length),
      h = new DataView(header.buffer);
    h.setUint32(0, 0x04034b50, true);
    h.setUint16(4, 20, true);
    h.setUint16(6, 0x800, true);
    h.setUint16(12, 33, true);
    h.setUint32(14, crc, true);
    h.setUint32(18, data.length, true);
    h.setUint32(22, data.length, true);
    h.setUint16(26, name.length, true);
    header.set(name, 30);
    const dir = new Uint8Array(46 + name.length),
      d = new DataView(dir.buffer);
    d.setUint32(0, 0x02014b50, true);
    d.setUint16(4, 20, true);
    d.setUint16(6, 20, true);
    d.setUint16(8, 0x800, true);
    d.setUint16(14, 33, true);
    d.setUint32(16, crc, true);
    d.setUint32(20, data.length, true);
    d.setUint32(24, data.length, true);
    d.setUint16(28, name.length, true);
    d.setUint32(42, offset, true);
    dir.set(name, 46);
    local.push(header, data);
    central.push(dir);
    offset += header.length + data.length;
    centralSize += dir.length;
  }
  const end = new Uint8Array(22),
    e = new DataView(end.buffer);
  e.setUint32(0, 0x06054b50, true);
  e.setUint16(8, files.length, true);
  e.setUint16(10, files.length, true);
  e.setUint32(12, centralSize, true);
  e.setUint32(16, offset, true);
  const result = new Uint8Array(offset + centralSize + 22);
  let position = 0;
  for (const part of [...local, ...central, end]) {
    result.set(part, position);
    position += part.length;
  }
  return result;
}

export function downloadGovernanceWorkbook(
  report: FindingMetricsReport,
  from: string,
  to: string,
  filter: FindingMetricsFilter,
  previous?: FindingMetricsReport,
  directory?: OrgDirectory,
) {
  const bytes = governanceWorkbook(governanceSheets(report, from, to, filter, previous, directory));
  const blob = new Blob([bytes as Uint8Array<ArrayBuffer>], {
    type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `artex-governance-${from}-${to}.xlsx`;
  link.click();
  URL.revokeObjectURL(url);
}
