"use client";

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  type FindingMetricsFilter,
  type FindingResponsibilityOptions,
  findingSeverityLabels,
} from "@/lib/finding-metrics";

export function FindingMetricsScope({
  filter,
  options,
  onChange,
  labelPrefix = "",
  hideDepartment = false,
  hideResponsibility = false,
}: {
  filter: FindingMetricsFilter;
  options: FindingResponsibilityOptions;
  onChange: (filter: FindingMetricsFilter) => void;
  labelPrefix?: string;
  hideDepartment?: boolean;
  hideResponsibility?: boolean;
}) {
  const directory = options.directory;
  const fields: { key: keyof FindingMetricsFilter; label: string; options: [string, string][]; id?: boolean }[] = [
    { key: "severity", label: "等级", options: Object.entries(findingSeverityLabels) },
  ];
  if (directory) {
    const label = (item: { name: string; active: boolean }) => `${item.name}${item.active ? "" : "（已停用）"}`;
    const department = directory.departments.find((item) => item.id === filter.department_id);
    const line = directory.business_lines.find((item) => item.id === filter.business_line_id);
    const ownerIDs = new Set(options.historical_owner_ids ?? []);
    for (const id of [
      line?.primary_id,
      line?.backup_id,
      department?.head_id,
      directory.departments.find((item) => item.id === line?.department_id)?.head_id,
      directory.fallback_id,
      filter.owner_id,
    ]) {
      if (id) ownerIDs.add(id);
    }
    if (!hideDepartment)
      fields.push({
        key: "department_id",
        label: "部门",
        id: true,
        options: [
          ["0", "部门未归属"],
          ...directory.departments.map((item): [string, string] => [String(item.id), label(item)]),
        ],
      });
    if (!hideResponsibility) {
      fields.push({
        key: "business_line_id",
        label: "业务线",
        id: true,
        options: [
          ["0", "业务线未归属"],
          ...directory.business_lines
            .filter(
              (item) =>
                filter.department_id == null ||
                item.department_id === filter.department_id ||
                options.historical_line_ids?.includes(item.id) ||
                item.id === filter.business_line_id,
            )
            .map((item): [string, string] => [String(item.id), label(item)]),
        ],
      });
      fields.push({
        key: "owner_id",
        label: "负责人",
        id: true,
        options: [
          ["0", "负责人未指派"],
          ...directory.people
            .filter(
              (item) =>
                ownerIDs.has(item.id) ||
                (filter.business_line_id == null &&
                  (filter.department_id == null || item.department_id === filter.department_id)),
            )
            .map((item): [string, string] => [String(item.id), label(item)]),
        ],
      });
    }
  } else if (!hideResponsibility) {
    fields.push({
      key: "business_line",
      label: "业务线",
      options: [["__none__", "业务线未归属"], ...options.business_lines.map((name): [string, string] => [name, name])],
    });
    fields.push({
      key: "assignee",
      label: "负责人",
      options: [["__none__", "负责人未指派"], ...options.assignees.map((name): [string, string] => [name, name])],
    });
  }
  return fields.map((field) => (
    <Select
      key={field.key}
      value={filter[field.key] == null || filter[field.key] === "" ? "__all__" : String(filter[field.key])}
      onValueChange={(value) => {
        const next = {
          ...filter,
          [field.key]: value === "__all__" ? (field.id ? undefined : "") : field.id ? Number(value) : value,
        };
        if (field.key === "department_id") {
          next.business_line_id = undefined;
          next.owner_id = undefined;
        }
        if (field.key === "business_line_id") next.owner_id = undefined;
        onChange(next);
      }}
    >
      <SelectTrigger aria-label={`${labelPrefix}${field.label}筛选`} className="w-36 shrink-0">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="__all__">全部{field.label}</SelectItem>
        {field.options.map(([value, label]) => (
          <SelectItem key={value} value={value}>
            {label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  ));
}
