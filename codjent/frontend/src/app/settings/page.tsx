import type { Metadata } from "next";
import { ProviderSettings } from "@/components/provider-settings";

export const metadata: Metadata = {
  title: "Provider settings · codjent",
  description: "Configure the model providers codjent can use.",
};

export default function SettingsPage() {
  return <ProviderSettings />;
}
