> 📜 **铸剑炉防守反击宪章**（用户原则）见 [CHARTER.md](CHARTER.md)

# 防守反击 · 安全响应体系 (Defense & Counter-Response)

纯防御性安全框架: 诱捕、检测、阻断、溯源、告警。**不发起任何主动攻击**。

## 组件
| 文件 | 功能 |
|---|---|
| honeypot.py | 蜜罐: 模拟SSH(2222)/HTTP(8080)诱捕攻击者, 只记录 |
| ids.py | 入侵检测: SSH暴力破解/端口扫描/攻击UA/路径爆破 规则引擎 |
| blocker.py | 自动阻断: 生成iptables/netsh命令, **默认dry_run只出命令** |
| tracer.py | 溯源: 攻击者指纹+时间线+风险评分 -> Markdown报告 |
| alert.py | 告警: 本地日志 + 可选Webhook/邮件(需配config.json) |
| main.py | 编排: `python main.py --once` 单轮 / 默认监控循环 |
| test_sim.py | 本地模拟测试(无真实流量) |

## 部署到VPS(Linux)
1. 上传本目录到服务器
2. `python3 main.py --once` 验证链路
3. `nohup python3 main.py > monitor.log 2>&1 &` 后台监控
4. 确认无误后改 config.json 中 `auto_block.dry_run: false` 启用真实阻断
5. 可选: `crontab` 定时执行 `python3 main.py --once`

## 法律与安全边界
- 蜜罐监听本机端口, 记录的是**主动来打你的人**, 全程合法
- 阻断命令作用于**自己的服务器**, 不反击任何第三方
- 溯源报告用于**取证/报案**, 交给网安部门处置
