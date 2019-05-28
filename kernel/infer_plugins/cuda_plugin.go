package main

/*
#cgo LDFLAGS: -lm -pthread
#cgo LDFLAGS:  -L../../ -lcvm_runtime_cuda -lcudart -lcuda
#cgo LDFLAGS: -lstdc++ 

#cgo CFLAGS: -I../include -I/usr/local/cuda/include/ -O2

#cgo CFLAGS: -Wall -Wno-unused-result -Wno-unknown-pragmas -Wno-unused-variable

#include <cvm/c_api.h>
*/
import "C"
import (
	"fmt"
//	"os"
//	"time"
	"errors"
	"unsafe"
//	"strings"
//	"strconv"
	"github.com/CortexFoundation/CortexTheseus/log"
)

func LoadModel(modelCfg, modelBin string,  deviceId int) (unsafe.Pointer, error) {
	net := C.CVMAPILoadModel(
		C.CString(modelCfg),
		C.CString(modelBin),
		1,
		C.int(deviceId),
	)

	if net == nil {
		return nil, errors.New("Model load error")
	}
	return net, nil
}

func GetModelOpsFromModel(net unsafe.Pointer) (int64, error) {
	ret := int64(C.CVMAPIGetGasFromModel(net))
	if ret < 0 {
		return 0, errors.New("Gas Error")
	} else {
		return ret, nil
	}
}

func GetModelOps(filepath string) (uint64, error) {
	ret := int64(C.CVMAPIGetGasFromGraphFile(C.CString(filepath)))
	if ret < 0 {
		return 0, errors.New("Gas Error")
	} else {
		return uint64(ret), nil
	}
}

func FreeModel(net unsafe.Pointer) {
	C.CVMAPIFreeModel(net)
}

func Predict(net unsafe.Pointer, imageData []byte) ([]byte, error) {
	if net == nil {
		return nil, errors.New("Internal error: network is null in InferProcess")
	}

	resLen := int(C.CVMAPIGetOutputLength(net))
	if resLen == 0 {
		return nil, errors.New("Model result len is 0")
	}

	res := make([]byte, resLen)

	flag := C.CVMAPIInfer(
		net,
		(*C.char)(unsafe.Pointer(&imageData[0])),
		(*C.char)(unsafe.Pointer(&res[0])))
	log.Info("Infernet", "flag", flag, "res", res)
	if flag != 0 {
		return nil, errors.New("Predict Error")
	}

	return res, nil
}

func InferCore(modelCfg, modelBin string, imageData []byte, deviceId int) (ret []byte, err error) {
	log.Info("infer on cuda", "........................")
	net, loadErr := LoadModel(modelCfg, modelBin, deviceId)
	defer FreeModel(net)
	if loadErr != nil {
		return nil, errors.New("Model load error")
	}
	expectedInputSize := int(C.CVMAPIGetInputLength(net))
	if expectedInputSize != len(imageData) {
		return nil, errors.New(fmt.Sprintf("input size not match, Expected: %d, Have %d",
																		  expectedInputSize, len(imageData)))
	}
	ret, err = Predict(net, imageData)
	return ret, err
}
