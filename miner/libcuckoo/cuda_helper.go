// +build cuda

package main

/*
#cgo LDFLAGS: -L./ -lcudaminer -L/usr/local/cuda/lib64 -lcudart -lstdc++ -lnvidia-ml
#cgo CFLAGS: -I./

#include "miner.h"
*/
import "C"
import (
	"fmt"
	"log"
	//	"time"
	"encoding/hex"
	"github.com/ethereum/go-ethereum/PoolMiner/common"
	"github.com/ethereum/go-ethereum/PoolMiner/config"
	"github.com/ethereum/go-ethereum/PoolMiner/crypto"
	"math/rand"
	"time"
	"unsafe"
)

func FindSolutionsByGPU(hash []byte, nonce uint64, threadId uint32) (nedges uint32) {
	var tmpHash = make([]byte, 32)
	copy(tmpHash[:], hash)

	//	start := time.Now()
	r := C.FindSolutionsByGPU(
		(*C.uint8_t)(unsafe.Pointer(&tmpHash[0])),
		C.uint64_t(nonce),
		C.uint32_t(threadId))

	//	duration := time.Since(start)
	//	log.Println(fmt.Sprintf("CuckooFindSolutionCuda | time=%v, status code=%v", duration, _numSols))

	// TODO add warning of discarding possible solutions
	return uint32(r)
}

func FindCycles(threadId uint32, nedges uint32) (status_code uint32, ret [][]uint32) {
	var (
		_solLength uint32
		_numSols   uint32
		result     [128]uint32
	)
	r := C.FindCycles(
		C.uint32_t(threadId),
		C.uint32_t(nedges),
		(*C.uint32_t)(unsafe.Pointer(&result[0])),
		C.uint32_t(len(result)),
		(*C.uint32_t)(unsafe.Pointer(&_solLength)),
		(*C.uint32_t)(unsafe.Pointer(&_numSols)))

	if uint32(len(result)) < _solLength*_numSols {
		log.Println(fmt.Sprintf("WARNING: discard possible solutions, total sol num=%v, received number=%v", _numSols, uint32(len(result))/_solLength))
		_numSols = uint32(len(result)) / _solLength
	}

	for solIdx := uint32(0); solIdx < _numSols; solIdx++ {
		var sol = make([]uint32, _solLength)
		copy(sol[:], result[solIdx*_solLength:(solIdx+1)*_solLength])
		//	 log.Println(fmt.Sprintf("Index: %v, Solution: %v", solIdx, sol))
		ret = append(ret, sol)
	}

	return uint32(r), ret
}

func CuckooInitialize(devices []uint32, deviceNum uint32, param config.Param) {
	C.CuckooInitialize((*C.uint32_t)(unsafe.Pointer(&devices[0])), C.uint32_t(deviceNum), C.int(param.Algorithm), 1)
}

func Monitor(device_count uint32) (fanSpeeds []uint32, temperatures []uint32) {
	var (
		_fanSpeeds    [128]uint32
		_temperatures [128]uint32
	)
	C.monitor(C.uint32_t(device_count), (*C.uint32_t)(unsafe.Pointer(&_fanSpeeds[0])), (*C.uint32_t)(unsafe.Pointer(&_temperatures[0])))
	for i := 0; i < int(device_count); i++ {
		fanSpeeds = append(fanSpeeds, _fanSpeeds[i])
		temperatures = append(temperatures, _temperatures[i])
	}
	return fanSpeeds, temperatures
}

//var nonceIndex int = 0
//var noncesOfFindSolution int = 0

func verifySolution(status uint32, sols [][]uint32, tgtDiff common.Hash, curNonce uint64, header []byte, taskHeader string, solChan chan config.Task, deviceInfos []config.DeviceInfo, param config.Param) {
	var result common.BlockSolution
	if status != 0 {
		//if verboseLevel >= 3 {
		//	log.Println("result: ", status, sols)
		//}
		//			noncesOfFindSolution += 1
		//			log.Println("nonceIndex=", nonceIndex,"nonce=",curNonce, "noncesOfFindSolution = ", noncesOfFindSolution)
		for _, solUint32 := range sols {
			var sol common.BlockSolution
			copy(sol[:], solUint32)
			sha3hash := common.BytesToHash(crypto.Sha3Solution(&sol))
			//if verboseLevel >= 3 {
			//	log.Println(curNonce, "\n sol hash: ", hex.EncodeToString(sha3hash.Bytes()), "\n tgt hash: ", hex.EncodeToString(tgtDiff.Bytes()))
			//}
			//				log.Println(tgtDiff.Big(), sha3hash.Big(), header[:], curNonce, sol)
			if sha3hash.Big().Cmp(tgtDiff.Big()) <= 0 {
				result = sol
				nonceStr := common.Uint64ToHexString(uint64(curNonce))
				digest := common.Uint32ArrayToHexString([]uint32(result[:]))
				var ok int
				if param.Algorithm == 0 {
					ok = CuckooVerifyProof(header[:], curNonce, &sol[0])
				} else {
					ok = CuckooVerifyProof_cuckaroo(header[:], curNonce, &sol[0])
				}
				if ok != 1 {
					log.Println("verify failed", header[:], curNonce, &sol)
				} else {
					log.Println("verify successed", header[:], curNonce, &sol)
					solChan <- config.Task{Nonce: nonceStr, Header: taskHeader, Solution: digest}
				}
			}
		}
	}
}

func RunSolver(THREAD int, deviceInfos []config.DeviceInfo, param config.Param, taskChan chan config.Task, solChan chan config.Task, state bool) (status_code uint32, ret [][]uint32) {
	rand.Seed(time.Now().UTC().UnixNano())
	nedgesChan := make(chan config.StreamData, THREAD)
	var taskNumber = make([]uint32, THREAD)
	for i := 0; i < THREAD; i++ {
		taskNumber[i] = uint32(0)
	}

	var exitCh = make([]chan string, THREAD)
	for i := 0; i < THREAD; i++ {
		exitCh[i] = make(chan string, 1)
	}

	var readyCh = make([]chan string, THREAD)
	for i := 0; i < THREAD; i++ {
		readyCh[i] = make(chan string, 1)
	}
	go func() {
		for {
			if state == false {
				log.Println("Exit task thread")
				return
			}

			select {
			case task := <-taskChan:
				for nthread := uint32(0); nthread < uint32(THREAD); nthread++ {
					tidx := uint32(nthread)
					taskNumber[tidx] = taskNumber[tidx] + 1
					if len(task.Difficulty) == 0 {
						return
					}
					header, _ := hex.DecodeString(task.Header[2:])
					curNonce := uint64(rand.Int63())
					if taskNumber[tidx] > 1 {
						go func(exitCh chan string) { exitCh <- "ping" }(exitCh[tidx])

						go func(tmp chan string, tmp1 chan string) {
							select {
							case exit := <-tmp1:
								if exit == "pong" {
								}
								for {
									select {
									case exit := <-tmp:
										if exit == "ping" {
											log.Println("Task thread quit [", tidx, "] task : ", taskNumber[tidx])
											tmp1 <- "pong"
											return
										}
									default:
										curNonce = uint64(curNonce + 1)
										deviceInfos[tidx].Lock.Lock()
										var nedges uint32 = FindSolutionsByGPU(header, curNonce, tidx)
										var streamData config.StreamData
										nedgesChan <- streamData.New(nedges, tidx, task.Difficulty, curNonce, header)
										deviceInfos[tidx].Lock.Unlock()
									}
								}
							}
						}(exitCh[tidx], readyCh[tidx])
						log.Println("New task", tidx, curNonce, header)
					} else {
						go func(tmp chan string, tmp1 chan string) {
							for {
								select {
								case exit := <-tmp:
									if exit == "ping" {
										log.Println("Task thread quit [", tidx, "] task : ", taskNumber[tidx])
										tmp1 <- "pong"
										return
									}
								default:
									curNonce = uint64(curNonce + 1)
									deviceInfos[tidx].Lock.Lock()
									var nedges uint32 = FindSolutionsByGPU(header, curNonce, tidx)
									var streamData config.StreamData
									nedgesChan <- streamData.New(nedges, tidx, task.Difficulty, curNonce, header)
									deviceInfos[tidx].Lock.Unlock()
								}
							}
						}(exitCh[tidx], readyCh[tidx])
						log.Println("New task init", tidx, curNonce, header)
					}
				}
			}
		}
	}()
	/*for nthread := 0; nthread < int(THREAD); nthread++ {
		go func(tidx uint32, currentTask_ *config.TaskWrapper) {
			number := uint32(0)
			for {
				if state == false {
					log.Println("Exit task thread")
					return
				}
				select {
				case task := <-taskChan:
					{

						if len(task.Difficulty) == 0 {
							time.Sleep(100 * time.Millisecond)
							continue
						}
						header, _ := hex.DecodeString(task.Header[2:])
						curNonce := uint64(rand.Int63())
						number = number + 1
						tmp := number
						go func(tmp uint32) {
							for {
								if tmp < number {
									log.Println("Task thread quit [", tidx, "] task", tmp, number)
									return
								}
								curNonce = uint64(curNonce + 1)
								deviceInfos[tidx].Lock.Lock()
								log.Println("lock", tidx)
								var nedges uint32 = FindSolutionsByGPU(header, curNonce, tidx)
								log.Println("unlock", tidx)
								deviceInfos[tidx].Lock.Unlock()
								var streamData config.StreamData
								nedgesChan <- streamData.New(nedges, tidx, task.Difficulty, curNonce, header)
							}
						}(tmp)
						log.Println("New task", tidx, curNonce, header)
					}
				default:
					continue
				}
			}
		}(uint32(nthread), &config.CurrentTask)
	}*/

	go func() {
		for {
			select {
			case streamData := <-nedgesChan:
				tidx := streamData.ThreadId
				//					log.Println(streamData)
				status, sols := FindCycles(tidx, streamData.Nedges)
				end_time := time.Now().UnixNano() / 1e6
				deviceInfos[tidx].Use_time = (end_time - deviceInfos[tidx].Start_time)
				deviceInfos[tidx].Solution_count += int64(len(sols))
				deviceInfos[tidx].Gps += 1
				tgtDiff := common.HexToHash(streamData.Difficulty[2:])
				curNonce := streamData.Nonce
				header := streamData.Header
				verifySolution(status, sols, tgtDiff, curNonce, header, config.CurrentTask.TaskQ.Header, solChan, deviceInfos, param)
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()
	return 0, ret
}

func CuckooVerifyProof(hash []byte, nonce uint64, result *uint32) int {
	tmpHash := hash
	r := C.CuckooVerifyProof(
		(*C.uint8_t)(unsafe.Pointer(&tmpHash[0])),
		C.uint64_t(uint(nonce)),
		(*C.uint32_t)(unsafe.Pointer((result))))
	return int(r)
}

func CuckooVerifyProof_cuckaroo(hash []byte, nonce uint64, result *uint32) int {
	tmpHash := hash
	r := C.CuckooVerifyProof_cuckaroo(
		(*C.uint8_t)(unsafe.Pointer(&tmpHash[0])),
		C.uint64_t(uint(nonce)),
		(*C.uint32_t)(unsafe.Pointer((result))))
	return int(r)
}
