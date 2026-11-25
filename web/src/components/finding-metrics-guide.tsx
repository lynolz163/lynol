"use client";

import { useEffect, useState } from "react";

import { Info } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api } from "@/lib/api";
import { type FindingSLA, findingSeverityLabels } from "@/lib/finding-metrics";

const definitions = {
  new: { title: "新增漏洞", text: "所选期间首次发现的漏洞数量。按北京时间统计。" },
  closed: {
    title: "完成关闭",
    text: "所选期间变为已修复、已处理或复测确认修复的次数。开发反馈单独记录，不计为关闭；重开后再次关闭计一次新的关闭。",
  },
  open: {
    title: "待处置存量",
    text: "截至所选范围末日，仍为待处理、处理中或已确认的漏洞。缺少完整历史快照时显示 —，不作为 0 处理。",
  },
  overdue: {
    title: "逾期漏洞",
    text: "待处置且超过 SLA 修复时限的漏洞。从首次发现起按自然日计时，重开沿用原发现时间。逾期列表按超期天数排序，默认展示前 10 条，可展开全部记录。",
  },
  sla: {
    title: "SLA 达标率",
    text: "全量口径：期间按时关闭次数 /（期间有效关闭次数 + 期末仍逾期未关闭的漏洞数）。尚未到期的待处置漏洞不算失败，也不提前算达标。小字保留已关闭口径：按时关闭 / 有效关闭。重开后再次关闭计新的关闭次数，历史关闭沿用当时规则。业务线、负责人为空仍按等级正常计时。存量未知时不展示全量达标率。图中目标线为 100%；达到目标为绿色，80% 至不足 100% 为橙色，低于 80% 为红色。",
  },
  retest: {
    title: "复测不通过率",
    text: "仍可复现次数 /（仍可复现次数 + 确认修复次数）。无结论、中止或执行失败不计入分母，存在时单独展示。",
  },
  quality: {
    title: "修复质量与返工",
    text: "一次修复通过率：已记录历史中首次有明确结论的复测通过数 / 首次有明确结论的复测数，按首次结论时间归入期间。无结论和执行失败不占首次。平均返工轮次：本期复测确认修复的处置周期，在关闭前累计复测不通过的次数 / 本期复测确认修复的周期数，包含零返工周期，且追溯跨期失败记录。返工耗时从本周期首次不通过算到确认修复，仅统计发生返工并已通过的周期；重开进入新周期，历史回填只能反映已保留的记录。",
  },
  dataQuality: {
    title: "数据质量",
    text: "业务线未归属与负责人未指派分别统计期末待处置漏洞，二者可能重叠，不能相加当作独立漏洞总数。缺少归属不会豁免 SLA，仍从首次发现按等级计时。",
  },
  trend: {
    title: "趋势与上期对比",
    text: "趋势按日、周或月汇总。浅色斜线柱表示非完整周期，当前周期标为进行中，提示中展示实际日期；截取的历史周期标为非完整周期。较上期比较紧邻当前范围之前、天数相同的期间；数量展示差值，比率展示百分点差值。关闭与达标率上升为改善，存量、逾期、不通过率上升为恶化；新增变化只表示发现量，不直接判断好坏。任一期间的每日汇总不完整，或分母为 0 时，不计算差值。",
  },
  duration: {
    title: "阶段耗时与 P90",
    text: "只统计起止时间均有记录的阶段，按结束时间归入期间。P90 表示 90% 的样本在该时长内完成，使用合并后的原始样本计算。整体闭环从首次发现算到关闭，单独展示。瓶颈为平均耗时最长的阶段，占比为该阶段平均耗时 / 四个阶段平均耗时之和；各阶段样本不同，这个占比不等于同一批漏洞的精确闭环时间占比。",
  },
  age: {
    title: "漏洞账龄与分布",
    text: "以所选范围末日为基准，按首次发现至该日的存活时间分组。仅包含待处置漏洞；分布按等级和漏洞类型统计。",
  },
  groups: {
    title: "业务线与负责人对比",
    text: "按逾期数、待处置数排序。逾期率为期末逾期 / 待处置；待处置为 0 时显示 —。SLA 使用全量口径。关闭和时效按事件发生时的归属统计。只有至少两个分组时才展示对应维度的对比。",
  },
  reports: {
    title: "周报 / 月报",
    text: "报告使用独立的生成范围，汇总上一个完整自然周（周一至周日）或自然月。历史报告保留生成时的结果；相同周期和范围重复生成会复用已有报告。手动生成不推送，定时计划按自身范围发送到配置的渠道，受通知总开关控制。",
  },
  history: {
    title: "历史数据与更新",
    text: "已有发现、下发、开发反馈和复测时间会回填。缺少时间的旧人工状态变化只登记状态基线，完整存量从历史采集启用后开始。每日 00:05 自动汇总，也可手动更新。补录时间会更新相关阶段统计，历史 SLA 规则及已生成报告保留。",
  },
};
export type FindingMetricTopic = keyof typeof definitions;
export type FindingGuideTopic = FindingMetricTopic | "rules";

export function FindingMetricInfo({
  topic,
  onOpen,
}: {
  topic: FindingMetricTopic;
  onOpen: (topic: FindingGuideTopic) => void;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={`${definitions[topic].title}说明`}
          onClick={() => onOpen(topic)}
          className="inline-flex size-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
        >
          <Info className="size-3.5" />
        </button>
      </TooltipTrigger>
      <TooltipContent>查看{definitions[topic].title}口径</TooltipContent>
    </Tooltip>
  );
}

export function FindingMetricsGuide({
  topic,
  onTopicChange,
  onSaved,
  historySince,
}: {
  topic: FindingGuideTopic | null;
  onTopicChange: (topic: FindingGuideTopic | null) => void;
  onSaved: () => Promise<void>;
  historySince?: string | null;
}) {
  const [rules, setRules] = useState<FindingSLA>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (topic !== "rules") return;
    let active = true;
    setError("");
    api.findingSLA().then(
      (value) => {
        if (active) setRules(value);
      },
      () => {
        if (active) setError("读取规则失败，请重新打开。");
      },
    );
    return () => {
      active = false;
    };
  }, [topic]);

  async function save() {
    if (!rules) return;
    if (Object.values(rules).some((days) => !Number.isInteger(days) || days < 1 || days > 365)) {
      setError("请填写 1 至 365 之间的整数天数。");
      return;
    }
    setSaving(true);
    try {
      await api.setFindingSLA(rules);
      toast.success("SLA 规则已保存");
      onTopicChange(null);
      await onSaved();
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setSaving(false);
    }
  }

  const topics = Object.keys(definitions) as FindingMetricTopic[];
  const selected = topic && topic !== "rules" ? topic : "trend";
  return (
    <Sheet
      open={topic !== null}
      onOpenChange={(open) => {
        if (!open) onTopicChange(null);
      }}
    >
      <SheetContent className="overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>SLA 规则 / 指标说明</SheetTitle>
          <SheetDescription>统计使用北京时间。</SheetDescription>
        </SheetHeader>
        <div className="space-y-5 px-4 pb-6">
          <Tabs
            value={topic === "rules" ? "rules" : "metrics"}
            onValueChange={(value) => onTopicChange(value === "rules" ? "rules" : "trend")}
          >
            <TabsList className="w-full">
              <TabsTrigger value="rules">SLA 规则</TabsTrigger>
              <TabsTrigger value="metrics">指标说明</TabsTrigger>
            </TabsList>
          </Tabs>
          {topic === "rules" ? (
            <div className="space-y-5">
              <p className="text-muted-foreground text-sm">
                从首次发现起按自然日计时。新规则用于后续汇总，历史日期保留当时的规则。
              </p>
              {rules ? (
                Object.entries(findingSeverityLabels).map(([grade, label]) => (
                  <label key={grade} className="flex items-center justify-between gap-3" htmlFor={`sla-${grade}`}>
                    <span>{label}</span>
                    <span className="flex items-center gap-2">
                      <Input
                        id={`sla-${grade}`}
                        aria-label={`${label} SLA 天数`}
                        type="number"
                        min={1}
                        max={365}
                        className="w-24"
                        value={rules[grade as keyof FindingSLA]}
                        onChange={(event) => setRules({ ...rules, [grade]: Number(event.target.value) })}
                      />
                      天
                    </span>
                  </label>
                ))
              ) : (
                <p className="text-muted-foreground">正在读取规则…</p>
              )}
              {error && (
                <p role="alert" className="text-destructive text-sm">
                  {error}
                </p>
              )}
              <Button disabled={saving || !rules} onClick={() => void save()}>
                {saving ? "保存中…" : "保存并更新统计"}
              </Button>
            </div>
          ) : (
            <div className="space-y-5">
              {[selected, ...topics.filter((key) => key !== selected)].map((key) => (
                <section key={key} className={key === selected ? "space-y-2 rounded-lg bg-muted p-3" : "space-y-2"}>
                  <h3 className="font-medium">{definitions[key].title}</h3>
                  <p className="text-muted-foreground text-sm leading-relaxed">{definitions[key].text}</p>
                  {key === "history" && historySince && (
                    <p className="text-muted-foreground text-xs">
                      历史采集启用于 {new Date(historySince).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai" })}
                    </p>
                  )}
                </section>
              ))}
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
