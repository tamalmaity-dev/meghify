import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * Formats a base URL for display without a scheme. Handles GitHub Enterprise
 * over http and sub-path installs; the old `replace("https://", "")` left the
 * scheme visible and produced `https://host/path/apps/...` for GHE.
 */
export function displayHost(value: string): string {
  const trimmed = value.trim();
  try {
    const url = new URL(trimmed);
    const path = url.pathname.replace(/\/+$/, "");
    return `${url.host}${path}`;
  } catch {
    return trimmed.replace(/^https?:\/\//i, "").replace(/\/+$/, "");
  }
}
