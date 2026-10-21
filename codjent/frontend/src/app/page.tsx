export default function Home() {
  return (
    <main className="flex flex-1 items-center justify-center bg-zinc-50 px-6 py-12 font-sans dark:bg-black">
      <section className="w-full max-w-lg rounded-2xl border border-zinc-200 bg-white p-8 shadow-sm dark:border-zinc-800 dark:bg-zinc-950">
        <p className="font-mono text-xs font-medium uppercase tracking-[0.2em] text-zinc-500 dark:text-zinc-400">
          codjent
        </p>
        <h1 className="mt-4 text-3xl font-semibold tracking-tight text-zinc-950 dark:text-zinc-50">
          The frontend is ready.
        </h1>
        <p className="mt-3 max-w-md text-sm leading-6 text-zinc-600 dark:text-zinc-400">
          Next.js, Tailwind CSS, and Base UI are installed. The agent interface
          will land here.
        </p>
      </section>
    </main>
  );
}
