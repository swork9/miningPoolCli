/*
miningPoolCli – open-source tonuniverse mining pool client

Copyright (C) 2021 tonuniverse.com

This file is part of miningPoolCli.

miningPoolCli is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

miningPoolCli is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with miningPoolCli.  If not, see <https://www.gnu.org/licenses/>.
*/

package main

import (
	"bytes"
	"math/big"
	"math/rand"
	"miningPoolCli/config"
	"miningPoolCli/utils/api"
	"miningPoolCli/utils/bocUtils"
	"miningPoolCli/utils/getMiner"
	"miningPoolCli/utils/gpuUtils"
	"miningPoolCli/utils/helpers"
	"miningPoolCli/utils/miniLogger"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// miner settings specific GPU
type minerLocalTaskSettings struct {
	gpuData      gpuUtils.GPUstruct
	seed         string
	complexity   string
	giverAddress string
	taskServerId int
}

type goMineProc struct {
	procStdout bytes.Buffer
	procStdErr bytes.Buffer
	status     chan struct{}
	deadinside bool
	minerLocalTaskSettings
}

var procMiners []goMineProc

func convertHexData(data string) string {
	n := new(big.Int)
	n.SetString(data, 16)
	return n.String()
}

func startMiner(goInt int) {
	// procMiners[goInt].status = false

	cmd := exec.Command(
		"./"+config.MinerGetter.MinerDirectory+"/pow-miner-opencl",
		"-vv",
		// "-w"+strconv.Itoa(config.StaticBeforeMinerSettings.NumCPUForWFlag), // num of threads
		"-g"+strconv.Itoa(procMiners[goInt].gpuData.GpuId),              // GPU id
		"-p"+strconv.Itoa(config.StaticBeforeMinerSettings.PlatformID),  // platform id
		"-F"+strconv.Itoa(config.StaticBeforeMinerSettings.BoostFactor), // boost factor
		"-t"+strconv.Itoa(config.StaticBeforeMinerSettings.TimeoutT),    // timeout insec
		config.StaticBeforeMinerSettings.PoolAddress,                    // pool address
		convertHexData(procMiners[goInt].seed),                          // seed
		convertHexData(procMiners[goInt].complexity),                    // complexity
		config.StaticBeforeMinerSettings.Iterations,                     // iterations
		procMiners[goInt].giverAddress,                                  // giver address
		config.MinerGetter.MinerDirectory+ // --------- output boc file "mined_{gpuID}.boc"
			"/mined_"+strconv.Itoa(procMiners[goInt].gpuData.GpuId)+".boc",
	)

	cmd.Stdout = &procMiners[goInt].procStdout
	cmd.Stderr = &procMiners[goInt].procStdErr

	cmd.Start()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	procMiners[goInt].status = make(chan struct{}, 1)

	defer func() {

		// Handle panic | fix "panic: close of closed channel"
		_ = recover()
	}()

	// fmt.Println("start task")
	select {
	case <-procMiners[goInt].status:
		_ = cmd.Process.Kill()
		close(procMiners[goInt].status)
		return
	case <-done:
		procMiners[goInt].deadinside = true
		// time.Sleep(10 * time.Second)
		// tasks := api.GetTasks().Tasks
		// setMinerTask(goInt, procMiners[goInt].gpuData, tasks)
		close(done)
		return
	}
}

func setMinerTask(index int, gpuData gpuUtils.GPUstruct, tasks []api.Task) {
	procMiners[index].deadinside = false
	randomTask := tasks[rand.Intn(len(tasks))]

	// fmt.Println("-------- set new task --------")
	// fmt.Println("seed: " + randomTask.Seed)
	// fmt.Println("gpuId: " + strconv.Itoa(gpuData.GpuId))
	// fmt.Println("-------- ------------ --------")

	procMiners[index].gpuData = gpuData
	procMiners[index].seed = randomTask.Seed
	procMiners[index].complexity = randomTask.NewComplexity
	procMiners[index].giverAddress = randomTask.Giver
	procMiners[index].taskServerId = randomTask.Id

	procMiners[index].procStdErr.Reset()
	procMiners[index].procStdout.Reset()

	go startMiner(index)
}

func checkTaskIsActual(tasks []api.Task, checkId int) bool {
	var found bool = false
	for i := 0; i < len(tasks); i++ {
		if tasks[i].Id == checkId {
			found = true
			break
		}
	}
	return found
}

func killAndStartWithNewTask(index int, gpuData gpuUtils.GPUstruct, pm *[]goMineProc) {
	// kill miner
	// procMiners[index].status = true
	procMiners[index].status <- struct{}{}

	// start this GPU with new task
	tasks := api.GetTasks().Tasks
	setMinerTask(index, gpuData, tasks)
}

func calcHashrate(goMineProcArr []goMineProc) {
	// var workAll = true
	// for i := 0; i < len(goMineProcArr); i++ {
	// 	if goMineProcArr[i].status {
	// 		workAll = false
	// 		break
	// 	}
	// }

	// if !workAll {
	// 	return
	// }

	var totalHashRate int

	for _, v := range goMineProcArr {
		hS := strings.Split(v.procStdout.String(), "\n")

		if len(hS) < 4 {
			return
		}
		lastH := hS[len(hS)-2]

		sep := strings.Split(lastH, ".")
		if len(sep) != 2 {
			return
		}

		perHashRate, err := strconv.Atoi(sep[0])
		if err != nil {
			return
		}

		totalHashRate += perHashRate
	}

	miniLogger.LogInfo("Total hashrate: ~" + strconv.Itoa(totalHashRate) + " Mh")

}

func main() {
	rand.Seed(time.Now().Unix())
	helpers.InitProgram()
	api.Auth()
	getMiner.UbubntuGetMiner()
	gpusArray := gpuUtils.SearchGpus()

	miniLogger.LogPass()
	gpuUtils.LogGpuList(gpusArray)
	miniLogger.LogInfo("Launching the mining processes...")

	gpusCount := len(gpusArray)

	procMiners = make([]goMineProc, gpusCount)

	tasks := api.GetTasks()

	for pCount := 0; pCount < gpusCount; pCount++ {
		setMinerTask(pCount, gpusArray[pCount], tasks.Tasks)
	}

	hashratePrintTime := time.Now().Unix()
	checkActualTime := time.Now().Unix()
	for {
		list := helpers.ExecuteSimpleCommand("ls", config.MinerGetter.MinerDirectory)
		listArr := strings.Split(string(list), "\n")

		if time.Now().Unix()-hashratePrintTime > 60 {
			calcHashrate(procMiners)
			hashratePrintTime = time.Now().Unix()
		}

		var toCheckActual bool
		var tasks api.GetTasksResponse

		if time.Now().Unix()-checkActualTime > 60*3 {
			tasks = api.GetTasks()
			toCheckActual = true
		}

		for i := 0; i < len(procMiners); i++ {
			if toCheckActual {
				if !checkTaskIsActual(tasks.Tasks, procMiners[i].taskServerId) && !procMiners[i].deadinside {
					// The task is no longer relevant
					killAndStartWithNewTask(i, gpusArray[i], &procMiners)
					continue
				}
			}

			tMined := "mined_" + strconv.Itoa(procMiners[i].gpuData.GpuId) + ".boc"

			var found bool = false
			if helpers.StringInSlice(tMined, listArr) {
				found = true

				pathToBocFile := config.MinerGetter.MinerDirectory + "/" + tMined

				// read file send boc to server
				bocFileInHex, _ := bocUtils.ReadBocFileToHex(pathToBocFile)

				// remove boc file
				if err := os.Remove(pathToBocFile); err != nil {
					miniLogger.LogFatal(err.Error())
				}

				// fmt.Println("-------- BOC SEND --------")
				// fmt.Println("seed: " + procMiners[i].seed)
				// fmt.Println("gpuId: " + strconv.Itoa(procMiners[i].gpuData.GpuId))
				// fmt.Println("-------- ------------ --------")

				bocServerResp := api.SendHexBocToServer(bocFileInHex, procMiners[i].seed)
				if bocServerResp.Data != "Found" || bocServerResp.Status != "ok" {
					miniLogger.LogPass()
					miniLogger.LogError("Share found but server didn't accept it")
					miniLogger.LogError("----- Server error response for task with id " + strconv.Itoa(procMiners[i].taskServerId) + ":")
					miniLogger.LogError("-Status: " + bocServerResp.Status)
					miniLogger.LogError("-Data: " + bocServerResp.Data)
					miniLogger.LogError("-Hash: " + bocServerResp.Hash)
					miniLogger.LogError("-Complexity: " + bocServerResp.Complexity)
					miniLogger.LogError("----- Local data")
					miniLogger.LogError("-Seed: " + procMiners[i].seed)
					miniLogger.LogError("-Complexity: " + procMiners[i].complexity)
					miniLogger.LogPass()
				} else {
					miniLogger.LogOk(
						"Share FOUND on \"" + procMiners[i].gpuData.Model + "\" | task id: " +
							strconv.Itoa(procMiners[i].taskServerId),
					)
				}

				killAndStartWithNewTask(i, gpusArray[i], &procMiners)
			}

			if procMiners[i].deadinside && !found {
				tasks := api.GetTasks().Tasks
				setMinerTask(i, procMiners[i].gpuData, tasks)
			}
		}

		time.Sleep(500 * time.Microsecond)
	}
}
