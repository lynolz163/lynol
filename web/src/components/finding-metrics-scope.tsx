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
}: {
  filter: FindingMetricsFilter;
  options: FindingResponsibilityOptions;
  onChange: (filter: FindingMetricsFilter) => void;
  labelPrefix?: string;
}) {
  const fields = [
    { key: "severity", label: "等级", options: Object.entries(findingSeverityLabels) },
    {
      key: "business_line",
      label: "业务线",
      options: [["__none__", "业务线未归属"], ...options.business_lines.map((name) => [name, name])],
    },
    {
      key: "assignee",
      label: "负责人",
      options: [["__none__", "负责人未指派"], ...options.assignees.map((name) => [name, name])],
    },
  ] as const;
  return fields.map((field) => (
    <Select
      key={field.key}
      value={filter[field.key] || "__all__"}
      onValueChange={(value) => onChange({ ...filter, [field.key]: value === "__all__" ? "" : value })}
    >
      <SelectTrigger aria-label={`${labelPrefix}${field.label}筛选`} className="w-32 shrink-0">
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
