import { createClient, ConnectError, Code } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { QueryClient } from "@tanstack/react-query";
import { ConsoleService } from "./gen/providah/v1/console_pb";
const transport = createConnectTransport({ baseUrl: "/api" });
const raw = createClient(ConsoleService, transport);
let refresh: Promise<unknown> | null = null;
export const api = createClient(
  ConsoleService,
  createConnectTransport({
    baseUrl: "/api",
    interceptors: [
      (next) => async (request) => {
        try {
          return await next(request);
        } catch (error) {
          if (
            ConnectError.from(error).code !== Code.Unauthenticated ||
            ["Login", "Refresh", "BeginSetup", "FinishSetup", "BeginOIDCLogin", "CompleteOIDCLogin"].includes(
              request.method.name,
            )
          )
            throw error;
          refresh ??= raw.refresh({}).finally(() => {
            refresh = null;
          });
          await refresh;
          return next(request);
        }
      },
    ],
  }),
);
export const queries = new QueryClient({
  defaultOptions: {
    queries: { retry: false, staleTime: 15_000 },
    mutations: { retry: false },
  },
});
export const message = (e: unknown) => ConnectError.from(e).rawMessage;
