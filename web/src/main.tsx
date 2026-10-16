import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider, createRouter } from "@tanstack/react-router";
import { QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { queryClient } from "@/lib/api";
import { routeTree } from "@/routeTree.gen";
import "@xterm/xterm/css/xterm.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={createRouter({ routeTree, context: { queryClient } })} />
      <Toaster position="bottom-right" theme="dark" richColors closeButton />
    </QueryClientProvider>
  </StrictMode>,
);
