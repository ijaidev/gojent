"use client";

import { Dialog } from "@base-ui/react/dialog";
import { ChevronLeft, ChevronRight, Download, X } from "lucide-react";
import { useRef } from "react";
import { cn } from "@/lib/utils";

export interface PreviewImage {
  url: string;
  filename?: string;
}

interface ImageLightboxProps {
  images: PreviewImage[];
  index: number | null;
  onIndexChange: (index: number | null) => void;
}

export function ImageLightbox({
  images,
  index,
  onIndexChange,
}: ImageLightboxProps) {
  const currentIndex = index ?? -1;
  const current = images[currentIndex];
  const open = Boolean(current);
  const multiple = images.length > 1;
  const closeButtonRef = useRef<HTMLButtonElement>(null);

  const go = (delta: number) => {
    if (!current || images.length < 2) {
      return;
    }
    onIndexChange((currentIndex + delta + images.length) % images.length);
  };

  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          onIndexChange(null);
        }
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-[60] bg-black/70 backdrop-blur-[3px] transition-opacity duration-150 data-ending-style:opacity-0 data-starting-style:opacity-0 motion-reduce:transition-none" />
        <Dialog.Popup
          className="fixed left-1/2 top-1/2 z-[60] flex h-[min(92dvh,900px)] w-[min(94vw,1240px)] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden overscroll-contain rounded-2xl border border-white/10 bg-[#111110] text-white shadow-[0_32px_100px_-20px_rgba(0,0,0,0.7)] outline-none transition duration-150 data-ending-style:scale-[0.985] data-ending-style:opacity-0 data-starting-style:scale-[0.985] data-starting-style:opacity-0 motion-reduce:transition-none"
          initialFocus={closeButtonRef}
          onKeyDown={(event) => {
            if (event.key === "ArrowLeft") {
              event.preventDefault();
              go(-1);
            }
            if (event.key === "ArrowRight") {
              event.preventDefault();
              go(1);
            }
          }}
        >
          <div className="flex shrink-0 items-center gap-3 border-b border-white/10 px-4 py-3">
            <div className="min-w-0">
              <Dialog.Title className="truncate text-[12.5px] font-medium text-white">
                {current?.filename || "Image"}
              </Dialog.Title>
              <Dialog.Description className="text-[10px] text-white/55">
                {multiple
                  ? `${currentIndex + 1} of ${images.length}`
                  : "Image preview"}
              </Dialog.Description>
            </div>
            <div className="ml-auto flex items-center gap-1">
              {current ? (
                <a
                  aria-label={`Download ${current.filename || "image"}`}
                  className="grid size-8 place-items-center rounded-lg text-white/65 transition hover:bg-white/10 hover:text-white"
                  download={current.filename || "image"}
                  href={current.url}
                >
                  <Download className="size-4" />
                </a>
              ) : null}
              <Dialog.Close
                aria-label="Close image preview"
                className="grid size-8 place-items-center rounded-lg text-white/65 transition hover:bg-white/10 hover:text-white"
                ref={closeButtonRef}
              >
                <X className="size-4" />
              </Dialog.Close>
            </div>
          </div>

          <div className="relative flex min-h-0 flex-1 items-center justify-center p-3 sm:p-6">
            {multiple ? (
              <button
                aria-label="Previous image"
                className="absolute left-2 top-1/2 z-10 grid size-10 -translate-y-1/2 place-items-center rounded-full border border-white/10 bg-black/45 text-white/80 backdrop-blur transition hover:bg-black/70 hover:text-white sm:left-4"
                onClick={() => go(-1)}
                type="button"
              >
                <ChevronLeft className="size-5" />
              </button>
            ) : null}

            {current ? (
              // biome-ignore lint/performance/noImgElement: the full-resolution preview uses data and provider URLs directly
              <img
                alt={current.filename || "Image preview"}
                className="max-h-full max-w-full rounded-lg object-contain"
                height={900}
                src={current.url}
                width={1400}
              />
            ) : null}

            {multiple ? (
              <button
                aria-label="Next image"
                className="absolute right-2 top-1/2 z-10 grid size-10 -translate-y-1/2 place-items-center rounded-full border border-white/10 bg-black/45 text-white/80 backdrop-blur transition hover:bg-black/70 hover:text-white sm:right-4"
                onClick={() => go(1)}
                type="button"
              >
                <ChevronRight className="size-5" />
              </button>
            ) : null}
          </div>

          {multiple ? (
            <div className="flex shrink-0 items-center justify-center gap-2 overflow-x-auto border-t border-white/10 px-4 py-2.5">
              {images.map((image, imageIndex) => (
                <button
                  aria-current={imageIndex === currentIndex}
                  aria-label={`Show image ${imageIndex + 1}${image.filename ? `: ${image.filename}` : ""}`}
                  className={cn(
                    "size-11 shrink-0 overflow-hidden rounded-md border transition",
                    imageIndex === currentIndex
                      ? "border-white/80 opacity-100"
                      : "border-white/10 opacity-55 hover:opacity-90",
                  )}
                  key={`${image.filename ?? "image"}-${imageIndex}`}
                  onClick={() => onIndexChange(imageIndex)}
                  type="button"
                >
                  {/* biome-ignore lint/performance/noImgElement: thumbnail URLs are already local data or provider URLs */}
                  <img
                    alt=""
                    className="h-full w-full object-cover"
                    height={44}
                    src={image.url}
                    width={44}
                  />
                </button>
              ))}
            </div>
          ) : null}
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
