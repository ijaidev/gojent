import type { ProviderFormat } from "@/lib/types";

export interface ProviderFormatInfo {
  value: ProviderFormat;
  label: string;
  shortLabel: string;
  description: string;
  baseURLPlaceholder: string;
}

export const providerFormats: ProviderFormatInfo[] = [
  {
    value: "openai-responses",
    label: "OpenAI Responses",
    shortLabel: "Responses",
    description:
      "OpenAI's Responses API, including gateways that implement it.",
    baseURLPlaceholder: "https://api.openai.com/v1",
  },
  {
    value: "openai-completion",
    label: "OpenAI Chat Completions",
    shortLabel: "Chat",
    description:
      "The widely supported /chat/completions API used by local and hosted models.",
    baseURLPlaceholder: "http://localhost:11434/v1",
  },
  {
    value: "anthropic",
    label: "Anthropic Messages",
    shortLabel: "Anthropic",
    description: "Claude's Messages API and compatible proxies.",
    baseURLPlaceholder: "https://api.anthropic.com",
  },
  {
    value: "google",
    label: "Google Gen AI",
    shortLabel: "Google",
    description: "Gemini's Generative Language API.",
    baseURLPlaceholder: "https://generativelanguage.googleapis.com",
  },
];

export function providerFormatInfo(format: ProviderFormat): ProviderFormatInfo {
  return (
    providerFormats.find((item) => item.value === format) ?? providerFormats[0]
  );
}
