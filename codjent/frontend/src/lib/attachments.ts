import type { AttachmentInput } from "@/lib/types";

export const MAX_ATTACHMENTS = 4;
export const MAX_IMAGE_BYTES = 5 * 1024 * 1024;
export const MAX_MESSAGE_BYTES = 8 * 1024 * 1024;
const MAX_IMAGE_EDGE = 2048;

const SUPPORTED_MIME = new Set<SupportedImageMIME>([
  "image/png",
  "image/jpeg",
  "image/webp",
]);

export type SupportedImageMIME = "image/png" | "image/jpeg" | "image/webp";

export interface PendingAttachment {
  id: string;
  filename: string;
  mimeType: SupportedImageMIME;
  size: number;
  data: string;
  previewURL: string;
}

export function toAttachmentInput(
  attachment: PendingAttachment,
): AttachmentInput {
  return {
    filename: attachment.filename,
    mime_type: attachment.mimeType,
    data: attachment.data,
  };
}

export function totalAttachmentBytes(attachments: PendingAttachment[]): number {
  return attachments.reduce((total, attachment) => total + attachment.size, 0);
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    const kilobytes = bytes / 1024;
    return `${kilobytes < 10 ? kilobytes.toFixed(1) : Math.round(kilobytes)} KB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export async function prepareAttachment(
  file: File,
): Promise<PendingAttachment> {
  const mimeType = imageMIME(file);
  if (!mimeType) {
    throw new Error(
      `${file.name || "This file"} is not a supported image. Use PNG, JPEG, or WebP.`,
    );
  }

  const normalized = await normalizeImage(file, mimeType);
  if (normalized.blob.size > MAX_IMAGE_BYTES) {
    throw new Error(
      `${normalized.filename} is still larger than ${formatBytes(MAX_IMAGE_BYTES)} after resizing.`,
    );
  }

  const data = await readBase64(normalized.blob);
  return {
    id: newID(),
    filename: normalized.filename,
    mimeType: normalized.mimeType,
    size: normalized.blob.size,
    data,
    previewURL: `data:${normalized.mimeType};base64,${data}`,
  };
}

function imageMIME(file: File): SupportedImageMIME | null {
  const declared = normalizeMIME(file.type);
  if (declared && SUPPORTED_MIME.has(declared)) {
    return declared;
  }
  const extension = file.name.split(".").pop()?.toLowerCase();
  switch (extension) {
    case "png":
      return "image/png";
    case "jpg":
    case "jpeg":
      return "image/jpeg";
    case "webp":
      return "image/webp";
    default:
      return null;
  }
}

function normalizeMIME(value: string): SupportedImageMIME | null {
  const mimeType = value.split(";")[0]?.trim().toLowerCase();
  if (mimeType === "image/jpg") {
    return "image/jpeg";
  }
  if (mimeType && SUPPORTED_MIME.has(mimeType as SupportedImageMIME)) {
    return mimeType as SupportedImageMIME;
  }
  return null;
}

async function normalizeImage(
  file: File,
  mimeType: SupportedImageMIME,
): Promise<{
  blob: Blob;
  mimeType: SupportedImageMIME;
  filename: string;
}> {
  const filename = file.name || defaultFilename(mimeType);
  if (typeof createImageBitmap !== "function") {
    return { blob: file, mimeType, filename };
  }

  let bitmap: ImageBitmap;
  try {
    bitmap = await createImageBitmap(file);
  } catch {
    throw new Error(`${filename} could not be read as an image.`);
  }

  try {
    const scale = Math.min(
      1,
      MAX_IMAGE_EDGE / Math.max(bitmap.width, bitmap.height),
    );
    const needsResize = scale < 1;
    const needsReencode = file.size > MAX_IMAGE_BYTES;
    if (!needsResize && !needsReencode) {
      return { blob: file, mimeType, filename };
    }

    const width = Math.max(1, Math.round(bitmap.width * scale));
    const height = Math.max(1, Math.round(bitmap.height * scale));
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext("2d");
    if (!context) {
      throw new Error("This browser cannot resize images.");
    }
    context.drawImage(bitmap, 0, 0, width, height);

    const outputMIME: SupportedImageMIME =
      mimeType === "image/png" ? "image/png" : "image/jpeg";
    let blob = await canvasBlob(canvas, outputMIME, 0.9);
    let finalMIME = outputMIME;

    // A large screenshot can remain over the limit as PNG. Prefer a readable
    // JPEG over rejecting it outright, but only when it actually helps.
    if (blob.size > MAX_IMAGE_BYTES && outputMIME === "image/png") {
      const fallback = await canvasBlob(canvas, "image/jpeg", 0.9);
      if (fallback.size < blob.size) {
        blob = fallback;
        finalMIME = "image/jpeg";
      }
    }

    return {
      blob,
      mimeType: finalMIME,
      filename: replaceExtension(filename, finalMIME),
    };
  } finally {
    bitmap.close();
  }
}

function canvasBlob(
  canvas: HTMLCanvasElement,
  type: SupportedImageMIME,
  quality: number,
): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob(
      (blob) => {
        if (blob) {
          resolve(blob);
          return;
        }
        reject(new Error("The image could not be encoded."));
      },
      type,
      quality,
    );
  });
}

function readBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error("The image could not be read."));
    reader.onload = () => {
      if (typeof reader.result !== "string") {
        reject(new Error("The image could not be read."));
        return;
      }
      const comma = reader.result.indexOf(",");
      if (comma < 0) {
        reject(new Error("The image could not be read."));
        return;
      }
      resolve(reader.result.slice(comma + 1));
    };
    reader.readAsDataURL(blob);
  });
}

function defaultFilename(mimeType: SupportedImageMIME): string {
  switch (mimeType) {
    case "image/png":
      return "image.png";
    case "image/jpeg":
      return "image.jpg";
    case "image/webp":
      return "image.webp";
  }
}

function replaceExtension(
  filename: string,
  mimeType: SupportedImageMIME,
): string {
  const base = filename.replace(/\.[^.]+$/, "");
  switch (mimeType) {
    case "image/png":
      return `${base}.png`;
    case "image/jpeg":
      return `${base}.jpg`;
    case "image/webp":
      return `${base}.webp`;
  }
}

function newID(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
