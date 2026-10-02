// 前端脚本的行为测试。
//
// 界面逻辑在 assets/index.html 里，没法用 Go 的测试框架覆盖。
// 但这个文件里最容易出错的是命令串解析（targetOf / expandVars）——
// 解析错了会给出一个点了就报错的按钮。所以把它抽出来用 node 断言。
//
// 用法：node internal/webui/assets/logic_test.js
const fs = require("fs");
const path = require("path");

const html = fs.readFileSync(path.join(__dirname, "index.html"), "utf8");

// 从 index.html 里抠出要测的函数，拼成一个可执行的片段。
function extract(name) {
  const re = new RegExp("function " + name + "\\s*\\([\\s\\S]*?\\n  \\}", "m");
  const m = html.match(re);
  if (!m) throw new Error("找不到函数 " + name);
  return m[0];
}

const src = [extract("expandVars"), extract("targetOf")].join("\n");
// eslint-disable-next-line no-new-func
const factory = new Function(src + "\nreturn { targetOf: targetOf, expandVars: expandVars };");
const { targetOf, expandVars } = factory();

let pass = 0;
let fail = 0;

function eq(label, got, want) {
  const g = JSON.stringify(got);
  const w = JSON.stringify(want);
  if (g === w) {
    pass++;
  } else {
    fail++;
    console.log("  ✗ " + label);
    console.log("      得到: " + g);
    console.log("      期望: " + w);
  }
}

function item(command, source, enabled) {
  return { command, source: source || "run-hkcu", enabled: enabled === undefined ? true : enabled };
}

console.log("expandVars");
eq("展开 %windir%", expandVars("%windir%\\system32\\a.exe"), "C:\\Windows\\system32\\a.exe");
eq("展开 %SystemRoot%（大小写不敏感）", expandVars("%SYSTEMROOT%\\x.exe"), "C:\\Windows\\x.exe");
eq("展开 %ProgramFiles%", expandVars("%ProgramFiles%\\a\\b.exe"), "C:\\Program Files\\a\\b.exe");
eq("展开 %ProgramFiles(x86)%", expandVars("%ProgramFiles(x86)%\\a.exe"), "C:\\Program Files (x86)\\a.exe");
eq("无变量时原样返回", expandVars("C:\\x\\y.exe"), "C:\\x\\y.exe");

console.log("targetOf —— 引号包起来的路径");
eq("带引号 + 参数",
  targetOf(item('"C:\\Program Files\\App\\a.exe" --auto')),
  { path: "C:\\Program Files\\App\\a.exe", kind: "file" });
eq("带引号无参数",
  targetOf(item('"D:\\ima.copilot\\ima.copilot.exe"')),
  { path: "D:\\ima.copilot\\ima.copilot.exe", kind: "file" });

console.log("targetOf —— 不带引号");
eq("裸路径 + 参数",
  targetOf(item("C:\\App\\b.exe --win-auto-start")),
  { path: "C:\\App\\b.exe", kind: "file" });
eq("裸路径无参数",
  targetOf(item("C:\\App\\c.exe")),
  { path: "C:\\App\\c.exe", kind: "file" });

console.log("targetOf —— 环境变量");
eq("%windir% 路径",
  targetOf(item("%windir%\\system32\\SecurityHealthSystray.exe")),
  { path: "C:\\Windows\\system32\\SecurityHealthSystray.exe", kind: "file" });

console.log("targetOf —— 启动文件夹（命令就是完整路径）");
eq(".lnk 文件",
  targetOf(item("C:\\Users\\x\\AppData\\Roaming\\Microsoft\\Windows\\Start Menu\\Programs\\Startup\\DeskPins.lnk", "startup-user")),
  { path: "C:\\Users\\x\\AppData\\Roaming\\Microsoft\\Windows\\Start Menu\\Programs\\Startup\\DeskPins.lnk", kind: "file" });

console.log("targetOf —— 应当返回 null 的情况");
eq("服务没有可打开的文件", targetOf(item("HKLM\\SYSTEM\\CurrentControlSet\\Services\\Foo", "service")), null);
eq("计划任务不处理", targetOf(item("\\Microsoft\\Windows\\Foo", "task")), null);
eq("已禁用的项不给按钮", targetOf(item("C:\\a\\b.exe", "run-hkcu", false)), null);
eq("空命令", targetOf(item("")), null);
eq("认不出来的命令", targetOf(item("rundll32.exe foo.dll,Bar")), null);

console.log("");
console.log(pass + " 通过, " + fail + " 失败");
process.exit(fail === 0 ? 0 : 1);
