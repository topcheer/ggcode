# Cron 终态去密集化（#5273）

循环哨兵 job 若长期零产出将永久烧轮次。`ConfigureNoDiff(jobID, N)` 启用阈值（默认 0=关闭零行为变化）；`RecordFireOutcome(jobID, producedDiff)` 逐轮回喂——产出轮清零 streak，空轮 +1；streak≥N（recurring）→自动 pause（PausedReason 记因、随 Job 持久化、重启不丢）；显式 wake=既有 `Resume()`。`JobIDForPrompt(prompt)` 为消费端 run→job 回接出口。
