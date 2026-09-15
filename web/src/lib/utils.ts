import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// img builds an artwork URL. TMDB stores relative paths joined with its
// CDN base + size; TVDB (shows, when a key is configured) serves absolute
// URLs that pass through untouched.
export function img(imageBase: string, size: string, path: string) {
  if (path.startsWith("http")) return path
  return `${imageBase}/${size}${path}`
}
