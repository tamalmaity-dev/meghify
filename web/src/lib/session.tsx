import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { Session, User } from "@/lib/types";

const sessionKey = ["session"] as const;

/**
 * Session state is queried on mount and kept for the lifetime of the tab. The
 * panel authenticates with a cookie, so there is nothing to persist client side.
 */
export function useSession() {
  const query = useQuery({
    queryKey: sessionKey,
    queryFn: () => api.get<Session>("/auth/session"),
    staleTime: 5 * 60_000,
    retry: false,
  });

  return {
    user: query.data?.user as User | undefined,
    authenticated: query.data?.authenticated === true,
    version: query.data?.version,
    isLoading: query.isLoading,
  };
}

export function useLogin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; password: string; code?: string }) =>
      api.post<User>("/auth/login", body),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: sessionKey }),
  });
}

export function useBootstrap() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; password: string; name: string }) =>
      api.post<User>("/auth/bootstrap", body),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: sessionKey }),
  });
}

export function useLogout() {
  return useMutation({ mutationFn: () => api.post("/auth/logout") });
}

export function useUpdateProfile() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string }) => api.put<User>("/me", body),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: sessionKey }),
  });
}

export function useChangePassword() {
  return useMutation({
    mutationFn: (body: { currentPassword: string; newPassword: string }) =>
      api.put("/me/password", body),
  });
}

export function useTotpEnroll() {
  return useMutation({
    mutationFn: () => api.get<{ uri: string; secret: string }>("/me/totp"),
  });
}

export function useTotpConfirm() {
  return useMutation({ mutationFn: (code: string) => api.post("/me/totp", { code }) });
}

export function useTotpDisable() {
  return useMutation({
    mutationFn: (password: string) => api.delete("/me/totp", { password }),
  });
}

export function useInvalidate() {
  return useQueryClient().invalidateQueries;
}
