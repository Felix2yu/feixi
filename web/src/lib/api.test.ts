import { afterEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";

function mockFetch(status: number, contentType: string, body: string) {
  const resp = new Response(status === 204 ? null : body, {
    status,
    ...(contentType ? { headers: { "content-type": contentType } } : {}),
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => resp),
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("响应解析", () => {
  // 渠道「发送测试」/「重新发送」/「清空事件」都回 text/plain 的 OK。
  // 早先按路径猜格式，把 OK 当 JSON 解析，通知明明发出去了却弹「发送失败」。
  it("text/plain 的 OK 原样返回，不当 JSON 解析", async () => {
    mockFetch(200, "text/plain; charset=utf-8", "OK");
    await expect(api.testChannel("c1")).resolves.toBe("OK");
  });

  it("application/json 照旧解析成对象", async () => {
    mockFetch(200, "application/json", '{"code":"c1","name":"邮件"}');
    await expect(api.updateChannel("c1", { name: "邮件" })).resolves.toMatchObject({ code: "c1" });
  });

  it("ping 正文就算长得像 JSON 也保持文本", async () => {
    mockFetch(200, "text/plain; charset=utf-8", '{"hello":1}');
    await expect(api.pingBody("c1", 1)).resolves.toBe('{"hello":1}');
  });

  it("204 空响应返回 undefined", async () => {
    mockFetch(204, "", "");
    await expect(api.deleteChannel("c1")).resolves.toBeUndefined();
  });
});

describe("错误解析", () => {
  it("JSON 错误取 error 字段", async () => {
    mockFetch(502, "application/json", '{"error":"对端拒绝"}');
    await expect(api.testChannel("c1")).rejects.toThrow("对端拒绝");
  });

  it("纯文本错误直接用作提示", async () => {
    mockFetch(503, "text/plain; charset=utf-8", "通知器未就绪");
    await api.retryNotification("n1").catch((e: unknown) => {
      expect(e).toBeInstanceOf(ApiError);
      expect((e as ApiError).message).toBe("通知器未就绪");
      expect((e as ApiError).status).toBe(503);
    });
  });
});
