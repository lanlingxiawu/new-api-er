/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import type { TFunction } from "i18next";
import {
  ChevronDown,
  Copy,
  ExternalLink,
  Play,
  Save,
  Search,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Combobox,
  ComboboxCollection,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxGroup,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
} from "@/components/ui/combobox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";

import {
  getPriceMonitorResults,
  getPriceMonitorStatus,
  runPriceMonitor,
  updateSystemOption,
} from "../api";
import { useSettingsSaveConfirmation } from "../components/settings-save-confirmation";
import type {
  PriceMonitorPriceCell,
  PriceMonitorPriceLane,
  PriceMonitorPriceTier,
  PriceMonitorSourceHeader,
} from "../types";

const MODEL_PRICING_SCOPE = "billing.model-pricing";
const PAGE_SIZE = 20;

const primaryComparisonFilters = [
  ["all", "All differences"],
  ["channel_official", "Channel vs official"],
  ["channel_platform", "Channel vs platform"],
  ["platform_official", "Platform vs official"],
] as const;

const additionalComparisonFilters = [
  ["input", "Input price differs"],
  ["output", "Output price differs"],
  ["cache", "Cache price differs"],
  ["billing", "Billing differs"],
  ["official_missing", "Official model missing"],
  ["channel_missing", "Channel price missing"],
  ["source_failed", "Source check failed"],
] as const;

type ComparisonFilter =
  | (typeof primaryComparisonFilters)[number][0]
  | (typeof additionalComparisonFilters)[number][0];

function SourceColumnPicker({
  headers,
  value,
  onChange,
  t,
}: {
  headers: PriceMonitorSourceHeader[];
  value: string[] | null;
  onChange: (value: string[] | null) => void;
  t: TFunction;
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [draftValue, setDraftValue] = useState<string[] | null>(value);
  const channels = useMemo(
    () => headers.filter((header) => header.type === "channel"),
    [headers],
  );
  const selectedCount = value === null ? channels.length : value.length;
  const draftKeys =
    draftValue === null ? channels.map((header) => header.key) : draftValue;
  const draftSelectedCount = draftKeys.length;
  const normalizedSearch = search.trim().toLowerCase();
  const filteredChannels = normalizedSearch
    ? channels.filter(
        (header) =>
          header.name.toLowerCase().includes(normalizedSearch) ||
          header.api_url?.toLowerCase().includes(normalizedSearch),
      )
    : channels;
  const label =
    value === null
      ? t("All channels")
      : t("{{count}} channels selected", { count: selectedCount });

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen);
    setSearch("");
    setDraftValue(value);
  };

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger
        render={
          <Button
            variant="outline"
            className="w-full justify-between font-normal"
          >
            <span className="truncate">{label}</span>
            <ChevronDown className="size-4 shrink-0 opacity-60" />
          </Button>
        }
      />
      <PopoverContent align="start" className="w-96 max-w-[90vw] gap-0 p-0">
        <div className="p-3">
          <div className="relative">
            <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2" />
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder={t("Search channels")}
              className="pl-9"
              autoFocus
            />
          </div>
          <div className="mt-2 flex gap-1">
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setDraftValue(null)}
            >
              {t("All channels")}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setDraftValue([])}>
              {t("Clear channels")}
            </Button>
          </div>
        </div>
        <Separator />
        <div className="max-h-72 overflow-y-auto overscroll-contain p-2">
          {filteredChannels.map((header) => {
            const checked = draftKeys.includes(header.key);
            return (
              <label
                key={header.key}
                className={cn(
                  "hover:bg-accent flex cursor-pointer items-start gap-3 rounded-md px-2 py-2.5",
                  checked && "bg-accent/50",
                )}
              >
                <Checkbox
                  checked={checked}
                  onCheckedChange={() => {
                    if (draftValue === null) {
                      setDraftValue(
                        channels
                          .filter((channel) => channel.key !== header.key)
                          .map((channel) => channel.key),
                      );
                      return;
                    }
                    setDraftValue(
                      checked
                        ? draftValue.filter((key) => key !== header.key)
                        : [...draftValue, header.key],
                    );
                  }}
                />
                <span className="min-w-0">
                  <span className="block truncate text-sm font-medium">
                    {header.name}
                  </span>
                  <span className="text-muted-foreground block break-all text-xs">
                    {header.api_url}
                  </span>
                </span>
              </label>
            );
          })}
          {filteredChannels.length === 0 && (
            <p className="text-muted-foreground px-3 py-8 text-center text-sm">
              {t("No matching channels")}
            </p>
          )}
        </div>
        <Separator />
        <div className="flex items-center justify-between gap-3 p-3">
          <Badge variant="secondary">
            {t("Selected {{count}}", { count: draftSelectedCount })}
          </Badge>
          <Button
            size="sm"
            onClick={() => {
              onChange(draftValue);
              setOpen(false);
            }}
          >
            {t("Apply")}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}

function ModelFilterCombobox({
  models,
  value,
  onValueChange,
  onSubmit,
  t,
}: {
  models: string[];
  value: string;
  onValueChange: (value: string) => void;
  onSubmit: (value: string) => void;
  t: TFunction;
}) {
  const selectedValue = models.includes(value) ? value : null;

  return (
    <Combobox
      items={models}
      value={selectedValue}
      inputValue={value}
      onInputValueChange={(nextValue) => onValueChange(nextValue)}
      onValueChange={(nextValue) => {
        if (!nextValue) return;
        onValueChange(nextValue);
        onSubmit(nextValue);
      }}
      filter={(model, query) =>
        model.toLowerCase().includes(query.trim().toLowerCase())
      }
      autoHighlight
    >
      <ComboboxInput
        id="price-monitor-model-filter"
        className="w-full"
        placeholder={t("Search or select a model")}
        showClear
        onKeyDown={(event) => {
          if (event.key !== "Enter" || selectedValue || !value.trim()) return;
          event.preventDefault();
          onSubmit(value.trim());
        }}
      />
      <ComboboxContent>
        <ComboboxList>
          <ComboboxGroup>
            <ComboboxCollection>
              {(model: string) => (
                <ComboboxItem key={model} value={model} className="py-2.5">
                  <span className="truncate font-medium">{model}</span>
                </ComboboxItem>
              )}
            </ComboboxCollection>
          </ComboboxGroup>
        </ComboboxList>
        <ComboboxEmpty>{t("No matching models")}</ComboboxEmpty>
      </ComboboxContent>
    </Combobox>
  );
}

function sourceStickyClass(
  type: PriceMonitorSourceHeader["type"],
  fixedIndex: number | undefined,
  layer: "header" | "body",
) {
  if (fixedIndex === undefined) return "";
  const zIndex = layer === "header" ? "lg:z-30" : "lg:z-20";
  const left = fixedIndex === 0 ? "lg:left-56" : "lg:left-[30rem]";
  const divider = type === "official" ? "border-r-2" : "";
  return `${divider} lg:sticky ${left} ${zIndex}`;
}

export type PriceMonitorDefaults = {
  enabled: boolean;
  intervalMinutes: number;
  timeoutSeconds: number;
  includeOfficial: boolean;
  includeModelsDev: boolean;
  modelWhitelist: string;
};

type PriceMonitorPanelProps = {
  defaults: PriceMonitorDefaults;
};

function formatPrice(value?: number) {
  if (value === undefined) return "—";
  const maximumFractionDigits =
    Math.abs(value) > 0 && Math.abs(value) < 0.000001 ? 10 : 6;
  return `$${value.toLocaleString(undefined, { maximumFractionDigits })}`;
}

function PriceLine({
  label,
  value,
  different,
  missingText,
}: {
  label: string;
  value?: number;
  different?: boolean;
  missingText?: string;
}) {
  return (
    <div
      className={
        different
          ? "bg-destructive/10 text-destructive flex items-center justify-between gap-4 rounded px-2 py-1"
          : "flex items-center justify-between gap-4 px-2 py-1"
      }
    >
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className="whitespace-nowrap font-semibold">
        {value === undefined ? (
          <span
            className={
              different
                ? "text-destructive text-xs font-medium"
                : "text-muted-foreground text-xs font-normal"
            }
          >
            {missingText ?? "—"}
          </span>
        ) : (
          formatPrice(value)
        )}
      </span>
    </div>
  );
}

function laneLabel(key: PriceMonitorPriceLane["key"], t: TFunction) {
  const labels: Record<string, string> = {
    cache_read: t("Cache read price"),
    cache_write: t("Cache write price"),
    cache_write_1h: t("Cache write price (1 hour)"),
    image_input: t("Image input price"),
    image_output: t("Image output price"),
    audio_input: t("Audio input price"),
    audio_output: t("Audio output price"),
  };
  return labels[key] ?? key;
}

function tierLabel(tier: PriceMonitorPriceTier, t: TFunction) {
  if (
    !tier.condition_variable ||
    !tier.condition_operator ||
    tier.condition_value === undefined
  ) {
    return t("All input lengths");
  }
  const variable =
    tier.condition_variable === "c" ? t("Output length") : t("Input length");
  return `${variable} ${tier.condition_operator} ${tier.condition_value.toLocaleString()} tokens`;
}

function PriceCell({
  price,
  sourceType,
}: {
  price?: PriceMonitorPriceCell;
  sourceType: PriceMonitorSourceHeader["type"];
}) {
  const { t } = useTranslation();
  if (!price) {
    return (
      <span className="text-muted-foreground text-sm">
        {sourceType === "channel"
          ? t("Model not enabled for this channel")
          : t("Price source did not provide this model")}
      </span>
    );
  }
  if (price.unavailable_reason) {
    const messages = {
      missing:
        sourceType === "official"
          ? t("Official price preset does not include this model")
          : t("Channel pricing API did not provide this model"),
      placeholder: t("Placeholder price excluded from comparison"),
      source_failed: t("Price source check failed; wait for the next check"),
    };
    return (
      <span
        className={
          price.different
            ? "text-destructive text-sm font-medium"
            : "text-muted-foreground text-sm"
        }
      >
        {messages[price.unavailable_reason]}
      </span>
    );
  }
  const lanes = (price.lanes ?? []).map((lane) => (
    <PriceLine
      key={lane.key}
      label={laneLabel(lane.key, t)}
      value={lane.price}
      different={lane.different}
    />
  ));
  if (price.mode === "per_token") {
    return (
      <div className="space-y-0.5">
        <PriceLine
          label={t("Input")}
          value={price.input}
          different={price.input_different}
        />
        <PriceLine
          label={t("Output")}
          value={price.output}
          different={price.output_different}
          missingText={t("Input price only")}
        />
        {lanes}
      </div>
    );
  }
  if (price.mode === "per_request") {
    return (
      <PriceLine
        label={t("Fixed price")}
        value={price.price}
        different={price.price_different}
      />
    );
  }
  if (price.tiers?.length) {
    return (
      <div className="space-y-2">
        {price.tiers.map((tier) => (
          <div
            key={`${tier.range}:${tier.condition_variable ?? ""}:${tier.condition_operator ?? ""}:${tier.condition_value ?? ""}`}
            className={
              price.mode_different
                ? "bg-destructive/10 rounded p-2"
                : "rounded border p-2"
            }
          >
            <p className="text-muted-foreground mb-1 text-xs">
              {tierLabel(tier, t)}
            </p>
            <PriceLine
              label={t("Input")}
              value={tier.input}
              different={price.mode_different}
            />
            <PriceLine
              label={t("Output")}
              value={tier.output}
              different={price.mode_different}
            />
            {(tier.lanes ?? []).map((lane) => (
              <PriceLine
                key={lane.key}
                label={laneLabel(lane.key, t)}
                value={lane.price}
                different={price.mode_different}
              />
            ))}
          </div>
        ))}
      </div>
    );
  }
  return (
    <div
      className={
        price.mode_different
          ? "bg-destructive/10 text-destructive rounded p-2"
          : "text-muted-foreground p-2"
      }
    >
      <p className="font-medium">{t("Dynamic rule pricing")}</p>
      <p className="mt-1 text-xs">
        {t("Price depends on request or time conditions")}
      </p>
    </div>
  );
}

export function PriceMonitorPanel({ defaults }: PriceMonitorPanelProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const requestSaveConfirmation = useSettingsSaveConfirmation();
  const [form, setForm] = useState(defaults);
  const [page, setPage] = useState(1);
  const [draftModel, setDraftModel] = useState("");
  const [runBaseline, setRunBaseline] = useState<{
    checkedAt: number;
    lastAttemptAt: number;
  } | null>(null);
  const [filters, setFilters] = useState<{
    model: string;
    sourceKeys: string[] | null;
    comparison: ComparisonFilter;
  }>({ model: "", sourceKeys: null, comparison: "all" });

  useEffect(() => setForm(defaults), [defaults]);

  const statusQuery = useQuery({
    queryKey: ["price-monitor-status"],
    queryFn: getPriceMonitorStatus,
    refetchInterval: runBaseline ? 1_000 : 15_000,
  });
  const resultsQuery = useQuery({
    queryKey: ["price-monitor-results", filters, page],
    queryFn: () =>
      getPriceMonitorResults({
        model: filters.model,
        source_keys:
          filters.sourceKeys === null
            ? undefined
            : filters.sourceKeys.join(","),
        comparison: filters.comparison,
        page,
        page_size: PAGE_SIZE,
      }),
    refetchInterval: 30_000,
    placeholderData: keepPreviousData,
  });

  const saveMutation = useMutation({
    mutationFn: async () => {
      const updates = [
        ["price_monitor_setting.enabled", form.enabled],
        ["price_monitor_setting.interval_minutes", form.intervalMinutes],
        ["price_monitor_setting.timeout_seconds", form.timeoutSeconds],
        ["price_monitor_setting.include_models_dev", form.includeModelsDev],
        ["price_monitor_setting.model_whitelist", form.modelWhitelist],
      ] as const;
      const responses = await Promise.all(
        updates.map(([key, value]) =>
          updateSystemOption({ scope: MODEL_PRICING_SCOPE, key, value }),
        ),
      );
      const failure = responses.find((response) => !response.success);
      if (failure) throw new Error(failure.message);
    },
    onSuccess: () => {
      toast.success(t("Price monitor settings saved"));
      queryClient.invalidateQueries({
        queryKey: ["system-options", MODEL_PRICING_SCOPE],
      });
      queryClient.invalidateQueries({ queryKey: ["price-monitor-status"] });
    },
    onError: (error: Error) =>
      toast.error(error.message || t("Failed to save price monitor settings")),
  });

  const runMutation = useMutation({
    mutationFn: runPriceMonitor,
    onMutate: () => {
      setRunBaseline({
        checkedAt: statusQuery.data?.data.snapshot.checked_at ?? 0,
        lastAttemptAt: statusQuery.data?.data.last_attempt_at ?? 0,
      });
    },
    onSuccess: (response) => {
      if (!response.success) {
        setRunBaseline(null);
        toast.error(response.message || t("Failed to start price check"));
        return;
      }
      toast.success(t("Price check started"));
      queryClient.invalidateQueries({ queryKey: ["price-monitor-status"] });
    },
    onError: (error: Error) => {
      setRunBaseline(null);
      toast.error(error.message || t("Failed to start price check"));
    },
  });

  const status = statusQuery.data?.data;
  const snapshot = status?.snapshot;
  const results = resultsQuery.data?.data;

  useEffect(() => {
    if (!runBaseline || !status || status.running) return;
    const snapshotUpdated = (snapshot?.checked_at ?? 0) > runBaseline.checkedAt;
    const attemptFinished = status.last_attempt_at > runBaseline.lastAttemptAt;
    if (!snapshotUpdated && !attemptFinished) return;

    setRunBaseline(null);
    void Promise.all([
      queryClient.invalidateQueries({ queryKey: ["price-monitor-status"] }),
      queryClient.invalidateQueries({ queryKey: ["price-monitor-results"] }),
    ]);
  }, [queryClient, runBaseline, snapshot?.checked_at, status]);

  const fixedSourcePositions = useMemo(() => {
    const positions = new Map<string, number>();
    let fixedIndex = 0;
    for (const header of results?.source_headers ?? []) {
      if (header.type !== "platform" && header.type !== "official") continue;
      positions.set(header.key, fixedIndex);
      fixedIndex += 1;
    }
    return positions;
  }, [results?.source_headers]);
  const shareURL = useMemo(
    () =>
      typeof window === "undefined"
        ? "/price_monitor/view"
        : `${window.location.origin}/price_monitor/view`,
    [],
  );

  const copyText = async (text: string, successKey: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast.success(t(successKey));
    } catch {
      toast.error(t("Copy failed"));
    }
  };

  const applyFilters = (model = draftModel.trim()) => {
    setPage(1);
    setFilters((current) => ({ ...current, model }));
  };

  const sourceLabel = (header: PriceMonitorSourceHeader) => {
    if (header.type === "platform") return t("Platform configuration");
    if (header.type === "official") return t("Official price");
    return header.name;
  };

  const sourceSubtitle = (header: PriceMonitorSourceHeader) => {
    if (header.type === "platform") return t("Platform baseline");
    if (header.type === "official") return t("Required comparison");
    return header.api_url || "";
  };

  return (
    <div className="flex min-h-0 min-w-0 max-w-full flex-col gap-6">
      <div className="grid min-w-0 gap-4 rounded-lg border p-4 md:grid-cols-2 xl:grid-cols-[repeat(5,minmax(0,1fr))]">
        <label className="flex items-center justify-between gap-3 rounded-md border px-3 py-2">
          <span className="text-sm font-medium">
            {t("Enable price monitor")}
          </span>
          <Switch
            checked={form.enabled}
            onCheckedChange={(enabled) =>
              setForm((current) => ({ ...current, enabled }))
            }
          />
        </label>
        <div className="grid gap-2">
          <Label htmlFor="price-monitor-interval">
            {t("Check interval (minutes)")}
          </Label>
          <Input
            id="price-monitor-interval"
            type="number"
            min={5}
            value={form.intervalMinutes}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                intervalMinutes: Number(event.target.value),
              }))
            }
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="price-monitor-timeout">
            {t("Source timeout (seconds)")}
          </Label>
          <Input
            id="price-monitor-timeout"
            type="number"
            min={1}
            max={120}
            value={form.timeoutSeconds}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                timeoutSeconds: Number(event.target.value),
              }))
            }
          />
        </div>
        <div className="flex flex-col justify-center rounded-md border px-3 py-2">
          <span className="text-sm font-medium">{t("Official price")}</span>
          <span className="text-muted-foreground text-xs">
            {t("Official prices are always compared")}
          </span>
        </div>
        <label className="flex items-center justify-between gap-3 rounded-md border px-3 py-2">
          <span className="text-sm font-medium">
            {t("Include models.dev prices")}
          </span>
          <Switch
            checked={form.includeModelsDev}
            onCheckedChange={(includeModelsDev) =>
              setForm((current) => ({ ...current, includeModelsDev }))
            }
          />
        </label>
        <div className="grid gap-2 md:col-span-2 xl:col-span-5">
          <Label htmlFor="price-monitor-model-whitelist">
            {t("Excluded model whitelist")}
          </Label>
          <Textarea
            id="price-monitor-model-whitelist"
            rows={4}
            value={form.modelWhitelist}
            placeholder={t("One model per line or separate with commas")}
            onChange={(event) =>
              setForm((current) => ({
                ...current,
                modelWhitelist: event.target.value,
              }))
            }
          />
          <p className="text-muted-foreground text-xs">
            {t(
              "Models in this list are skipped; leave empty to check all marketplace models",
            )}
          </p>
        </div>
        <div className="flex items-center gap-2 md:col-span-2 xl:col-span-5">
          <Button
            onClick={() =>
              requestSaveConfirmation(() => saveMutation.mutateAsync())
            }
            disabled={
              saveMutation.isPending ||
              form.intervalMinutes < 5 ||
              form.timeoutSeconds < 1 ||
              form.timeoutSeconds > 120
            }
          >
            <Save className="size-4" />
            {t("Save settings")}
          </Button>
          <Button
            variant="outline"
            onClick={() => runMutation.mutate()}
            disabled={
              runMutation.isPending ||
              runBaseline !== null ||
              status?.running ||
              !status?.is_master
            }
          >
            <Play className="size-4" />
            {status?.running || runBaseline
              ? t("Checking prices")
              : t("Check now")}
          </Button>
        </div>
      </div>

      <div className="grid min-w-0 gap-3 md:grid-cols-2 xl:grid-cols-[repeat(4,minmax(0,1fr))]">
        <div className="rounded-lg border p-4">
          <p className="text-muted-foreground text-sm">{t("Last checked")}</p>
          <p className="mt-1 font-medium">
            {snapshot?.checked_at
              ? new Date(snapshot.checked_at * 1000).toLocaleString()
              : t("Not checked yet")}
          </p>
        </div>
        <div className="rounded-lg border p-4">
          <p className="text-muted-foreground text-sm">
            {t("Different models")}
          </p>
          <p className="mt-1 text-xl font-semibold">
            {snapshot?.model_count ?? 0}
          </p>
        </div>
        <div className="rounded-lg border p-4">
          <p className="text-muted-foreground text-sm">
            {t("Difference items")}
          </p>
          <p className="mt-1 text-xl font-semibold">
            {snapshot?.item_count ?? 0}
          </p>
        </div>
        <div className="rounded-lg border p-4">
          <p className="text-muted-foreground text-sm">
            {t("Pricing sources")}
          </p>
          <p className="mt-1 font-medium">
            {snapshot ? `${snapshot.source_ok}/${snapshot.source_total}` : "—"}
          </p>
        </div>
      </div>

      {status?.last_attempt_error && (
        <p className="text-destructive text-sm">
          {t("Last price check failed. Review server logs and retry.")}
        </p>
      )}

      <div className="grid min-w-0 gap-3 rounded-lg border p-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
        <div className="grid gap-2">
          <Label>{t("Share page")}</Label>
          <div className="flex gap-2">
            <Input value={shareURL} readOnly />
            <Button
              variant="outline"
              size="icon"
              onClick={() => copyText(shareURL, "Share link copied")}
            >
              <Copy className="size-4" />
            </Button>
          </div>
        </div>
        <div className="grid gap-2">
          <Label>{t("Current access password")}</Label>
          <div className="flex gap-2">
            <Input value={snapshot?.access_password ?? ""} readOnly />
            <Button
              variant="outline"
              size="icon"
              disabled={!snapshot?.access_password}
              onClick={() =>
                copyText(
                  snapshot?.access_password ?? "",
                  "Access password copied",
                )
              }
            >
              <Copy className="size-4" />
            </Button>
          </div>
        </div>
        <Button
          variant="outline"
          className="self-end"
          render={<a href={shareURL} target="_blank" rel="noreferrer" />}
        >
          <ExternalLink className="size-4" />
          {t("Open share page")}
        </Button>
      </div>

      <div className="rounded-lg border p-3">
        <div className="grid gap-3 lg:grid-cols-[minmax(18rem,1.15fr)_minmax(18rem,1fr)_auto] lg:items-end">
          <div className="grid min-w-0 gap-2">
            <Label htmlFor="price-monitor-model-filter">
              {t("Model filter")}
            </Label>
            <ModelFilterCombobox
              models={results?.available_models ?? []}
              value={draftModel}
              onValueChange={setDraftModel}
              onSubmit={(model) => applyFilters(model)}
              t={t}
            />
          </div>
          <div className="grid min-w-0 gap-2">
            <Label>{t("Displayed channels")}</Label>
            <SourceColumnPicker
              headers={results?.available_source_headers ?? []}
              value={filters.sourceKeys}
              t={t}
              onChange={(sourceKeys) => {
                setPage(1);
                setFilters((current) => ({ ...current, sourceKeys }));
              }}
            />
          </div>
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => applyFilters()}>
              {t("Apply filters")}
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setDraftModel("");
                setPage(1);
                setFilters({
                  model: "",
                  sourceKeys: null,
                  comparison: "all",
                });
              }}
            >
              {t("Reset")}
            </Button>
          </div>
        </div>
        <div className="mt-3 flex min-h-8 flex-wrap items-center gap-2">
          {primaryComparisonFilters.map(([value, label]) => (
            <Button
              key={value}
              size="sm"
              variant={filters.comparison === value ? "default" : "outline"}
              onClick={() => {
                setPage(1);
                setFilters((current) => ({ ...current, comparison: value }));
              }}
            >
              {t(label)}
            </Button>
          ))}
          <Select
            items={additionalComparisonFilters.map(([value, label]) => ({
              value,
              label: t(label),
            }))}
            value={
              additionalComparisonFilters.some(
                ([value]) => value === filters.comparison,
              )
                ? filters.comparison
                : null
            }
            onValueChange={(value) => {
              if (!value) return;
              setPage(1);
              setFilters((current) => ({
                ...current,
                comparison: value as ComparisonFilter,
              }));
            }}
          >
            <SelectTrigger size="sm" className="w-44">
              <SelectValue placeholder={t("More filters")} />
            </SelectTrigger>
            <SelectContent align="start" alignItemWithTrigger={false}>
              <SelectGroup>
                {additionalComparisonFilters.map(([value, label]) => (
                  <SelectItem key={value} value={value} className="py-2">
                    {t(label)}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
          <p className="text-muted-foreground ml-auto text-sm">
            {t("All token prices are per million")}
          </p>
        </div>
      </div>

      <div
        className="min-h-[32rem] min-w-0 max-w-full overflow-hidden rounded-lg border"
        aria-busy={resultsQuery.isFetching}
      >
        <Table
          className="min-w-max"
          containerClassName="min-h-[32rem] max-w-full overflow-x-auto"
        >
          <TableHeader>
            <TableRow>
              <TableHead className="bg-muted sticky left-0 z-30 w-56 min-w-56 max-w-56">
                {t("Model")}
              </TableHead>
              {(results?.source_headers ?? []).map((header) => (
                <TableHead
                  key={header.key}
                  className={`bg-muted w-64 min-w-64 max-w-64 align-top ${sourceStickyClass(header.type, fixedSourcePositions.get(header.key), "header")}`}
                >
                  <span className="block break-all font-semibold">
                    {sourceLabel(header)}
                  </span>
                  <span className="text-muted-foreground mt-0.5 block text-xs font-normal">
                    {sourceSubtitle(header)}
                  </span>
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {(results?.items ?? []).map((item) => {
              return (
                <TableRow key={item.model}>
                  <TableCell className="bg-background sticky left-0 z-20 w-56 min-w-56 max-w-56 align-top font-semibold">
                    {item.model}
                  </TableCell>
                  {(results?.source_headers ?? []).map((header) => (
                    <TableCell
                      key={header.key}
                      className={`bg-background w-64 min-w-64 max-w-64 align-top ${sourceStickyClass(header.type, fixedSourcePositions.get(header.key), "body")}`}
                    >
                      <PriceCell
                        price={item.prices[header.key]}
                        sourceType={header.type}
                      />
                    </TableCell>
                  ))}
                </TableRow>
              );
            })}
            {!resultsQuery.isLoading && (results?.items.length ?? 0) === 0 && (
              <TableRow>
                <TableCell
                  colSpan={1 + (results?.source_headers.length ?? 0)}
                  className="text-muted-foreground text-center"
                >
                  {t("No price differences found")}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      <div className="flex items-center justify-between gap-3">
        <span className="text-muted-foreground text-sm">
          {t("{{total}} items", { total: results?.total ?? 0 })}
        </span>
        <div className="flex gap-2">
          <Button
            variant="outline"
            disabled={page <= 1}
            onClick={() => setPage((current) => current - 1)}
          >
            {t("Previous")}
          </Button>
          <Button
            variant="outline"
            disabled={!results || page * PAGE_SIZE >= results.total}
            onClick={() => setPage((current) => current + 1)}
          >
            {t("Next")}
          </Button>
        </div>
      </div>
    </div>
  );
}
