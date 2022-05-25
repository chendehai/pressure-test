package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"sync"
	"time"

	"github.com/33cn/chain33/rpc/grpcclient"
	chainTypes "github.com/33cn/chain33/types"
	chainUtil "github.com/33cn/chain33/util"
	"github.com/chendehai/pressure-test/chain33/evm/call"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v2"
)

type Job struct {
	Parameter    string
	Privkey      string
	ContractAddr string
}

type Conf struct {
	PoolSize        int      `yaml:"PoolSize"`
	OperationType   int      `yaml:"OperationType"`
	Rate            int      `yaml:"Rate"`
	GroupSie        int      `yaml:"GroupSie"`
	GrpcTxNum       int      `yaml:"GrpcTxNum"`
	DeployerPrivkey string   `yaml:"DeployerPrivkey"`
	DeployerAddr    string   `yaml:"DeployerAddr"`
	RpcType         int      `yaml:"RpcType"` // 1:jsonrpc  2:grpc
	MainChain       string   `yaml:"MainChain"`
	Chain           []string `yaml:"Chain"`
	ParaName        []string `yaml:"ParaName"`
	ContractAddr    []string `yaml:"ContractAddr"`
}

type Addr struct {
	Address string
	PrivKey string
}

var AddressList []Addr

var (
	configFile  = flag.String("f", "etc/config.yaml", "the config file")
	addressFile = flag.String("a", "etc/address.json", "the address file")
)

func main() {
	fmt.Println("开始测试")
	flag.Parse()

	content, err := ioutil.ReadFile(*configFile)
	if err != nil {
		fmt.Println("ioutil.ReadFile err: ", err)
	}

	var c Conf
	err = yaml.Unmarshal(content, &c)
	if err != nil {
		fmt.Println("读取配置失败 err:", err)
		return
	}

	err = InitAddress(*addressFile)
	if err != nil {
		fmt.Println("初始化地址失败", err)
		return
	}

	if c.RpcType == 2 {

		grpcJobChain1 := make(chan *chainTypes.Transaction, 5000)
		grpcJobChain2 := make(chan *chainTypes.Transaction, 5000)
		grpcJobChain3 := make(chan *chainTypes.Transaction, 5000)
		grpcJobChain4 := make(chan *chainTypes.Transaction, 5000)

		cha := []chan *chainTypes.Transaction{grpcJobChain1, grpcJobChain2, grpcJobChain3, grpcJobChain4}
		InitGrpcJobChain(cha, c.ContractAddr[0], c.ParaName[0], c.DeployerPrivkey, c.OperationType, c.Rate)
		time.Sleep(3 * time.Second)

		for i := 0; i < len(cha); i++ {
			go Send(c.PoolSize, c.Chain[0], cha[i])
		}

		time.Sleep(10 * time.Minute)
		return
	}

	if c.RpcType == 3 {
		start := time.Now()

		nftId := 0
		r := c.Rate/len(c.ContractAddr) + 1
		job3 := make([][][]*chainTypes.Transaction, 0, len(c.ContractAddr))
		for k := 0; k < len(c.ContractAddr); k++ {
			jobLists := make([][]*chainTypes.Transaction, 0, r)
			for i := 0; i < r; i++ {
				l := make([]*chainTypes.Transaction, 0, len(AddressList))
				jobLists = append(jobLists, l)
			}

			id := InitGrpcJobList(nftId, jobLists, c.ContractAddr[k], c.ParaName[k], c.DeployerPrivkey, c.OperationType, r, c.GroupSie)
			nftId = id
			job3 = append(job3, jobLists)
		}

		stop := time.Now()
		fmt.Println("交易构造完毕，开始发送, 构造开始时间: ", start.String(), "结束时间: ", stop.String(), "耗时：", stop.Unix()-start.Unix())
		for h := 0; h < len(job3); h++ {
			for j := 0; j < len(job3[h]); j++ {
				go SendList(c.Chain[h], job3[h][j], c.GrpcTxNum)
			}
		}

		time.Sleep(10 * time.Minute)
		return
	}

	if c.RpcType == 4 {
		contractAddrLen := len(c.ParaName)

		contractAddrs := make([]string, 0, contractAddrLen)
		for i := 0; i < contractAddrLen; i++ {
			d := &call.DeployContract{
				Endpoint:     c.Chain[i],
				ParaName:     c.ParaName[i],
				Bin:          bin,
				Abi:          abi,
				DeployerPri:  chainUtil.HexToPrivkey(c.DeployerPrivkey),
				DeployerAddr: c.DeployerAddr,
			}

			_, contractAddr, err := d.Deploy()
			if err != nil {
				panic(err)
			}
			contractAddrs = append(contractAddrs, contractAddr)
		}
		c.ContractAddr = contractAddrs

		start := time.Now()
		nftId := 0
		r := c.Rate / len(c.ContractAddr)
		if c.Rate%len(c.ContractAddr) != 0 {
			r++
		}
		job3 := make([][][]*chainTypes.Transaction, 0, len(c.ContractAddr))

		poolSize := 6
		wg := &sync.WaitGroup{}
		wg.Add(poolSize * len(c.ContractAddr))

		groupChains := make([]chan *TxGroupParams, 0, contractAddrLen)
		resultChains := make([]chan *chainTypes.Transaction, 0, contractAddrLen)

		job3Len := 0
		for k := 0; k < len(c.ContractAddr); k++ {
			jobLists := make([][]*chainTypes.Transaction, 0, r)
			for i := 0; i < r; i++ {
				l := make([]*chainTypes.Transaction, 0, len(AddressList))
				jobLists = append(jobLists, l)
				job3Len++
			}
			job3 = append(job3, jobLists)

			groupChain := make(chan *TxGroupParams, 1000)
			groupChains = append(groupChains, groupChain)
			resultChain := make(chan *chainTypes.Transaction, 1000)
			resultChains = append(resultChains, resultChain)

			go PollCreateTxGroup(poolSize, c.ContractAddr[k], c.ParaName[k], c.DeployerPrivkey, groupChains[k], resultChains[k], wg)
			go ChainToJobList(resultChains[k], job3[k])
			go InitGrpcTxGroupChain(nftId, c.OperationType, r, c.GroupSie, groupChains[k])

			nftId += len(AddressList) * r

		}
		wg.Wait()
		time.Sleep(1 * time.Second)

		createStop := time.Now()
		fmt.Println("交易构造完毕，开始发送, 构造开始时间: ", start.String(), "结束时间: ", createStop.String(), "耗时：", createStop.Unix()-start.Unix())

		wg.Add(job3Len)
		for h := 0; h < len(job3); h++ {
			for j := 0; j < len(job3[h]); j++ {
				go SendListWaitGroup(c.Chain[h], job3[h][j], c.GrpcTxNum, wg)
			}
		}

		wg.Wait()
		sendStop := time.Now()
		fmt.Println("交易发送完毕，发送结束时间", sendStop.String(), "耗时：", sendStop.Unix()-createStop.Unix())
		time.Sleep(100 * time.Second)
		return
	}

	if c.RpcType == 5 {
		contractAddrLen := len(c.ParaName)

		contractAddrs := make([]string, 0, contractAddrLen)
		for i := 0; i < contractAddrLen; i++ {
			d := &call.DeployContract{
				Endpoint:     c.Chain[i],
				ParaName:     c.ParaName[i],
				Bin:          bin,
				Abi:          abi,
				DeployerPri:  chainUtil.HexToPrivkey(c.DeployerPrivkey),
				DeployerAddr: c.DeployerAddr,
			}

			_, contractAddr, err := d.Deploy()
			if err != nil {
				panic(err)
			}
			contractAddrs = append(contractAddrs, contractAddr)
		}
		c.ContractAddr = contractAddrs
		fmt.Println("contractAddrs", contractAddrs)
		time.Sleep(10 * time.Second)

		start := time.Now()

		nftId := 0
		r := c.Rate / contractAddrLen
		if c.Rate%contractAddrLen != 0 {
			r++
		}

		poolSize := 6
		wg := &sync.WaitGroup{}
		wg.Add(poolSize * contractAddrLen)

		groupChains := make([]chan *TxGroupParams, 0, contractAddrLen)
		resultChains := make([]chan []*chainTypes.Transaction, 0, contractAddrLen)

		for k := 0; k < len(c.ContractAddr); k++ {

			groupChain := make(chan *TxGroupParams, 5000)
			groupChains = append(groupChains, groupChain)
			resultChain := make(chan []*chainTypes.Transaction, 5000)
			resultChains = append(resultChains, resultChain)

			go PollCreateTxGroupTxs(poolSize, c.ContractAddr[k], c.ParaName[k], c.DeployerPrivkey, groupChains[k], resultChains[k], c.GrpcTxNum, wg)

			go InitGrpcTxGroupChain(nftId, c.OperationType, r, c.GroupSie, groupChains[k])

			nftId += len(AddressList) * r

		}

		go func(wg *sync.WaitGroup) {
			wg.Wait()
			for h := 0; h < contractAddrLen; h++ {
				close(resultChains[h])
			}
		}(wg)

		time.Sleep(10 * time.Second)
		fmt.Println("开始发送交易")
		wgSend := &sync.WaitGroup{}
		wgSend.Add(contractAddrLen)
		for h := 0; h < contractAddrLen; h++ {
			go SendChainWaitGroup(c.MainChain, resultChains[h], wgSend)
		}

		wgSend.Wait()
		sendStop := time.Now()
		fmt.Println("交易发送完毕，发送结束时间", sendStop.String(), "耗时：", sendStop.Unix()-start.Unix())
		time.Sleep(100 * time.Second)
		return
	}
}

func InitAddress(addressFile string) error {
	content, err := ioutil.ReadFile(addressFile)
	if err != nil {
		return err
	}

	AddressList = make([]Addr, 0, 10000)
	err = json.Unmarshal(content, &AddressList)
	if err != nil {
		return err
	}
	return nil
}

func InitGrpcJobChain(grpcJobChain []chan *chainTypes.Transaction, contractAddr, paraName, deployerPrivkey string, operationType, rate int) {
	go func(grpcJobChain []chan *chainTypes.Transaction, contractAddr, paraName, deployerPrivkey string, operationType, rate int) {
		nftId := 0
		c := &call.CallContract{
			ContractAddr: contractAddr,
			ParaName:     paraName,
			Abi:          abi,
			DeployerPri:  chainUtil.HexToPrivkey(deployerPrivkey),
		}

		if operationType == 1 {
			for i := 0; i < len(AddressList); i++ {
				for j := 0; j < rate; j++ {
					nftId++
					tx, err := c.LocalCreateYCCEVMTx(fmt.Sprintf("mint(%q, %v)", AddressList[i].Address, nftId))
					if err != nil {
						fmt.Println("c.LocalCreateYCCEVMTx ,err: ", err)
						continue
					}
					y := nftId % len(grpcJobChain)
					grpcJobChain[y] <- tx
				}
			}
		} else if operationType == 2 {
			for i := 0; i < len(AddressList); i++ {
				ids := []int{nftId + 1, nftId + 2, nftId + 3, nftId + 4, nftId + 5}
				nftId += 5

				idsByte, _ := json.Marshal(ids)
				tx, err := c.LocalCreateYCCEVMTx(fmt.Sprintf("batchMint(%q, %v)", AddressList[i].Address, string(idsByte)))
				if err != nil {
					fmt.Println("c.LocalCreateYCCEVMTx ,err: ", err)
					continue
				}
				y := nftId % len(grpcJobChain)
				grpcJobChain[y] <- tx
			}
		} else if operationType == 3 {
			addrLen := len(AddressList)
			for i := 0; i < addrLen; i++ {
				for j := 0; j < rate; j++ {
					nftId++
					tx, err := c.LocalCreateYCCEVMTx(fmt.Sprintf("transfer(%q, %q, %v)", AddressList[i].Address, AddressList[addrLen-1-i].Address, nftId))
					if err != nil {
						fmt.Println("c.LocalCreateYCCEVMTx ,err: ", err)
						continue
					}
					y := nftId % len(grpcJobChain)
					grpcJobChain[y] <- tx
				}
			}
		} else if operationType == 4 {
			addrLen := len(AddressList)
			for i := 0; i < addrLen; i++ {
				ids := []int{nftId + 1, nftId + 2, nftId + 3, nftId + 4, nftId + 5}
				nftId += 5

				idsByte, _ := json.Marshal(ids)

				tx, err := c.LocalCreateYCCEVMTx(fmt.Sprintf("batchTransfer(%q, %q, %v)", AddressList[i].Address, AddressList[addrLen-1-i].Address, string(idsByte)))
				if err != nil {
					fmt.Println("c.LocalCreateYCCEVMTx ,err: ", err)
					continue
				}
				y := nftId % len(grpcJobChain)
				grpcJobChain[y] <- tx

			}
		}
	}(grpcJobChain, contractAddr, paraName, deployerPrivkey, operationType, rate)
}

type TxGroupParams struct {
	Params   []string
	Privkeys []string
}

func PollCreateTxGroup(poolSize int, contractAddr, paraName, deployerPrivkey string, groupChain chan *TxGroupParams, resultChain chan *chainTypes.Transaction, wg *sync.WaitGroup) {
	c := &call.CallContract{
		ContractAddr: contractAddr,
		ParaName:     paraName,
		Abi:          abi,
		DeployerPri:  chainUtil.HexToPrivkey(deployerPrivkey),
	}

	for i := 0; i < poolSize; i++ {
		go func(c *call.CallContract, groupChain chan *TxGroupParams, wg *sync.WaitGroup) {
			for param := range groupChain {
				tx, err := c.LocalCreateYCCEVMGroupTx(param.Params, param.Privkeys)
				if err != nil {
					fmt.Println("c.LocalCreateYCCEVMGroupTx ,err: ", err)
					continue
				}
				resultChain <- tx
			}
			wg.Done()
			fmt.Println("PollCreateTxGroup over")
		}(c, groupChain, wg)
	}
}

func PollCreateTxGroupTxs(poolSize int, contractAddr, paraName, deployerPrivkey string, groupChain chan *TxGroupParams, resultChain chan []*chainTypes.Transaction, grpcTxNum int, wg *sync.WaitGroup) {
	c := &call.CallContract{
		ContractAddr: contractAddr,
		ParaName:     paraName,
		Abi:          abi,
		DeployerPri:  chainUtil.HexToPrivkey(deployerPrivkey),
	}

	for i := 0; i < poolSize; i++ {
		go func(c *call.CallContract, groupChain chan *TxGroupParams, wg *sync.WaitGroup) {
			txs := make([]*chainTypes.Transaction, 0, grpcTxNum)
			for param := range groupChain {
				tx, err := c.LocalCreateYCCEVMGroupTx(param.Params, param.Privkeys)
				if err != nil {
					fmt.Println("c.LocalCreateYCCEVMGroupTx ,err: ", err)
					continue
				}
				txs = append(txs, tx)
				if len(txs) >= grpcTxNum {
					resultChain <- txs
					txs = make([]*chainTypes.Transaction, 0, grpcTxNum)
				}
			}

			if len(txs) >= 0 {
				resultChain <- txs
			}

			wg.Done()
		}(c, groupChain, wg)
	}
}

func ChainToJobList(resultChain chan *chainTypes.Transaction, jobLists [][]*chainTypes.Transaction) {
	groupCount := 0
	jobListsLen := len(jobLists)
	for tx := range resultChain {
		groupCount++
		y := groupCount % jobListsLen
		jobLists[y] = append(jobLists[y], tx)
	}
}

func InitGrpcTxGroupChain(nftId, operationType, rate, groupSize int, groupChain chan *TxGroupParams) {
	txCount := 0
	params := make([]string, 0, groupSize)
	privkeys := make([]string, 0, groupSize)

	if operationType == 1 {
		for i := 0; i < len(AddressList); i++ {
			for j := 0; j < rate; j++ {
				nftId++
				txCount++
				params = append(params, fmt.Sprintf("mint(%q, %v)", AddressList[i].Address, nftId))

				if txCount >= groupSize {
					param := &TxGroupParams{
						Params:   params,
						Privkeys: privkeys,
					}
					groupChain <- param

					txCount = 0
					params = make([]string, 0, groupSize)
				}

			}
		}
		close(groupChain)
	} else if operationType == 3 {
		addrLen := len(AddressList)
		for i := 0; i < addrLen; i++ {
			for j := 0; j < rate; j++ {
				nftId++
				txCount++
				params = append(params, fmt.Sprintf("transfer(%q, %q, %v)", AddressList[i].Address, AddressList[addrLen-1-i].Address, nftId))
				privkeys = append(privkeys, AddressList[i].PrivKey)

				if txCount >= groupSize {
					param := &TxGroupParams{
						Params:   params,
						Privkeys: privkeys,
					}
					groupChain <- param

					txCount = 0
					params = make([]string, 0, groupSize)
				}
			}
		}
		fmt.Println("InitGrpcTxGroupChain over1")
		close(groupChain)
		fmt.Println("InitGrpcTxGroupChain over2")
	}
}

func InitGrpcJobList(nftId int, jobLists [][]*chainTypes.Transaction, contractAddr, paraName, deployerPrivkey string, operationType, rate, groupSize int) int {
	c := &call.CallContract{
		ContractAddr: contractAddr,
		ParaName:     paraName,
		Abi:          abi,
		DeployerPri:  chainUtil.HexToPrivkey(deployerPrivkey),
	}

	txCount := 0
	params := make([]string, 0, groupSize)
	privkeys := make([]string, 0, groupSize)
	groupCount := 0
	if operationType == 1 {
		for i := 0; i < len(AddressList); i++ {
			for j := 0; j < rate; j++ {
				nftId++
				txCount++
				params = append(params, fmt.Sprintf("mint(%q, %v)", AddressList[i].Address, nftId))

				if txCount >= groupSize {
					tx, err := c.LocalCreateYCCEVMGroupTx(params, privkeys)
					if err != nil {
						fmt.Println("c.LocalCreateYCCEVMGroupTx ,err: ", err)
						continue
					}
					groupCount++
					y := groupCount % len(jobLists)
					jobLists[y] = append(jobLists[y], tx)

					txCount = 0
					params = make([]string, 0, groupSize)
				}
			}
		}
	} else if operationType == 2 {
		for i := 0; i < len(AddressList); i++ {
			ids := []int{nftId + 1, nftId + 2, nftId + 3, nftId + 4, nftId + 5}
			nftId += 5

			idsByte, _ := json.Marshal(ids)

			txCount++
			params = append(params, fmt.Sprintf("batchMint(%q, %v)", AddressList[i].Address, string(idsByte)))

			if txCount >= groupSize {
				tx, err := c.LocalCreateYCCEVMGroupTx(params, privkeys)
				if err != nil {
					fmt.Println("c.LocalCreateYCCEVMGroupTx ,err: ", err)
					continue
				}
				groupCount++
				y := groupCount % len(jobLists)
				jobLists[y] = append(jobLists[y], tx)

				txCount = 0
				params = make([]string, 0, groupSize)
			}
		}
	} else if operationType == 3 {
		addrLen := len(AddressList)
		for i := 0; i < addrLen; i++ {
			for j := 0; j < rate; j++ {
				nftId++
				txCount++
				params = append(params, fmt.Sprintf("transfer(%q, %q, %v)", AddressList[i].Address, AddressList[addrLen-1-i].Address, nftId))
				privkeys = append(privkeys, AddressList[i].PrivKey)

				if txCount >= groupSize {
					tx, err := c.LocalCreateYCCEVMGroupTx(params, privkeys)
					if err != nil {
						fmt.Println("c.LocalCreateYCCEVMGroupTx ,err: ", err)
						continue
					}
					groupCount++
					y := groupCount % len(jobLists)
					jobLists[y] = append(jobLists[y], tx)

					txCount = 0
					params = make([]string, 0, groupSize)
					privkeys = make([]string, 0, groupSize)
				}
			}
		}
	} else if operationType == 4 {
		addrLen := len(AddressList)
		for i := 0; i < addrLen; i++ {
			ids := []int{nftId + 1, nftId + 2, nftId + 3, nftId + 4, nftId + 5}
			nftId += 5

			idsByte, _ := json.Marshal(ids)

			txCount++
			params = append(params, fmt.Sprintf("batchTransfer(%q, %q, %v)", AddressList[i].Address, AddressList[addrLen-1-i].Address, string(idsByte)))
			privkeys = append(privkeys, AddressList[i].PrivKey)

			if txCount >= groupSize {
				tx, err := c.LocalCreateYCCEVMGroupTx(params, privkeys)
				if err != nil {
					fmt.Println("c.LocalCreateYCCEVMGroupTx ,err: ", err)
					continue
				}
				groupCount++
				y := groupCount % len(jobLists)
				jobLists[y] = append(jobLists[y], tx)

				txCount = 0
				params = make([]string, 0, groupSize)
				privkeys = make([]string, 0, groupSize)
			}

		}
	}
	return nftId
}

func SendChainWaitGroup(endpoint string, jobChan chan []*chainTypes.Transaction, wg *sync.WaitGroup) {
	defer wg.Done()

	maxMsgSize := 20 * 1024 * 1024 // 最大传输数据 最大区块大小
	diaOpt := grpc.WithDefaultCallOptions(
		grpc.MaxCallRecvMsgSize(maxMsgSize),
		grpc.MaxCallSendMsgSize(maxMsgSize),
	)

	conn, err := grpc.Dial(grpcclient.NewMultipleURL(endpoint), grpc.WithInsecure(), diaOpt)
	if err != nil {
		fmt.Println("grpcclient.NewMultipleURL err:", err)
		return
	}

	client := chainTypes.NewChain33Client(conn)

	for i := 0; i < 150; i++ {
		txs, ok := <-jobChan
		if !ok {
			return
		}
		replys, err := client.SendTransactions(context.Background(), &chainTypes.Transactions{Txs: txs})
		if err != nil {
			fmt.Println("SendTransaction err:", err, "txs:", txs)
			continue
		}
		fmt.Println("SendTransactions replys, isOK: ", replys.ReplyList[0].IsOk, "i=", i)
	}

	for txs := range jobChan {
		time.Sleep(200 * time.Millisecond)
		replys, err := client.SendTransactions(context.Background(), &chainTypes.Transactions{Txs: txs})
		if err != nil {
			fmt.Println("SendTransaction err:", err, "txs:", txs)
			continue
		}
		fmt.Println("SendTransactions replys, isOK: ", replys.ReplyList[0].IsOk)
	}
}

func SendListWaitGroup(endpoint string, jobList []*chainTypes.Transaction, grpcTxNum int, wg *sync.WaitGroup) {
	maxMsgSize := 20 * 1024 * 1024 // 最大传输数据 最大区块大小
	diaOpt := grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMsgSize),
		grpc.MaxCallSendMsgSize(maxMsgSize))

	conn, err := grpc.Dial(grpcclient.NewMultipleURL(endpoint), grpc.WithInsecure(), diaOpt)
	if err != nil {
		fmt.Println("grpcclient.NewMultipleURL err:", err)
		return
	}

	//go func(client chainTypes.Chain33Client, txs []*chainTypes.Transaction) {
	//	replys, err := client.SendTransactions(context.Background(), &chainTypes.Transactions{Txs: txs})
	//
	//	if err != nil {
	//		fmt.Println("SendTransaction err:", err)
	//	}
	//	fmt.Println("SendTransactions replys, isOK: ", replys.ReplyList[0].IsOk)
	//}(client, jobList[i : i+grpcTxNum])
	client := chainTypes.NewChain33Client(conn)
	for i := 0; i < len(jobList); i += grpcTxNum {
		replys, err := client.SendTransactions(context.Background(), &chainTypes.Transactions{Txs: jobList[i : i+grpcTxNum]})
		if err != nil {
			fmt.Println("SendTransaction err:", err)
		}
		fmt.Println("SendTransactions replys, isOK: ", replys.ReplyList[0].IsOk)
	}
	wg.Done()
}

func SendList(endpoint string, jobList []*chainTypes.Transaction, grpcTxNum int) {
	maxMsgSize := 20 * 1024 * 1024 // 最大传输数据 最大区块大小
	diaOpt := grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMsgSize),
		grpc.MaxCallSendMsgSize(maxMsgSize))

	conn, err := grpc.Dial(grpcclient.NewMultipleURL(endpoint), grpc.WithInsecure(), diaOpt)
	if err != nil {
		fmt.Println("grpcclient.NewMultipleURL err:", err)
		return
	}
	client := chainTypes.NewChain33Client(conn)
	for i := 0; i < len(jobList); i += grpcTxNum {
		replys, err := client.SendTransactions(context.Background(), &chainTypes.Transactions{Txs: jobList[i : i+grpcTxNum]})
		if err != nil {
			fmt.Println("SendTransaction err:", err)
		}
		fmt.Println("SendTransactions replys, isOK: ", replys.ReplyList[0].IsOk)
	}
}

func Send(poolSize int, endpoint string, grpcJobChain chan *chainTypes.Transaction) {
	for i := 0; i < poolSize; i++ {
		go func(endpoint string, grpcJobChain chan *chainTypes.Transaction) {
			conn, err := grpc.Dial(grpcclient.NewMultipleURL(endpoint), grpc.WithInsecure())
			if err != nil {
				fmt.Println("grpcclient.NewMultipleURL err:", err)
				return
			}
			client := chainTypes.NewChain33Client(conn)

			for tx := range grpcJobChain {
				reply, err := client.SendTransaction(context.Background(), tx)
				if err != nil {
					fmt.Println("SendTransaction err:", err)
					continue
				}
				fmt.Println("SendTransactions replys, isOK: ", reply.IsOk)
			}
		}(endpoint, grpcJobChain)
	}
}

const (
	abi = `[{"inputs":[],"stateMutability":"nonpayable","type":"constructor"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"account","type":"address"},{"indexed":true,"internalType":"address","name":"operator","type":"address"},{"indexed":false,"internalType":"bool","name":"approved","type":"bool"}],"name":"ApprovalForAll","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"operator","type":"address"},{"indexed":true,"internalType":"address","name":"from","type":"address"},{"indexed":true,"internalType":"address","name":"to","type":"address"},{"indexed":false,"internalType":"uint256[]","name":"ids","type":"uint256[]"},{"indexed":false,"internalType":"uint256[]","name":"values","type":"uint256[]"}],"name":"TransferBatch","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"operator","type":"address"},{"indexed":true,"internalType":"address","name":"from","type":"address"},{"indexed":true,"internalType":"address","name":"to","type":"address"},{"indexed":false,"internalType":"uint256","name":"id","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"value","type":"uint256"}],"name":"TransferSingle","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"string","name":"value","type":"string"},{"indexed":true,"internalType":"uint256","name":"id","type":"uint256"}],"name":"URI","type":"event"},{"inputs":[{"internalType":"address","name":"account","type":"address"},{"internalType":"uint256","name":"id","type":"uint256"}],"name":"balanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address[]","name":"accounts","type":"address[]"},{"internalType":"uint256[]","name":"ids","type":"uint256[]"}],"name":"balanceOfBatch","outputs":[{"internalType":"uint256[]","name":"","type":"uint256[]"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"owner","type":"address"},{"internalType":"uint256[]","name":"ids","type":"uint256[]"}],"name":"batchMint","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"from","type":"address"},{"internalType":"address","name":"to","type":"address"},{"internalType":"uint256[]","name":"ids","type":"uint256[]"}],"name":"batchTransfer","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"getSuccessNum","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"account","type":"address"},{"internalType":"address","name":"operator","type":"address"}],"name":"isApprovedForAll","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"to","type":"address"},{"internalType":"uint256","name":"id","type":"uint256"}],"name":"mint","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"from","type":"address"},{"internalType":"address","name":"to","type":"address"},{"internalType":"uint256[]","name":"ids","type":"uint256[]"},{"internalType":"uint256[]","name":"amounts","type":"uint256[]"},{"internalType":"bytes","name":"data","type":"bytes"}],"name":"safeBatchTransferFrom","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"from","type":"address"},{"internalType":"address","name":"to","type":"address"},{"internalType":"uint256","name":"id","type":"uint256"},{"internalType":"uint256","name":"amount","type":"uint256"},{"internalType":"bytes","name":"data","type":"bytes"}],"name":"safeTransferFrom","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"operator","type":"address"},{"internalType":"bool","name":"approved","type":"bool"}],"name":"setApprovalForAll","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"successNum","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"bytes4","name":"interfaceId","type":"bytes4"}],"name":"supportsInterface","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"address","name":"from","type":"address"},{"internalType":"address","name":"to","type":"address"},{"internalType":"uint256","name":"id","type":"uint256"}],"name":"transfer","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"","type":"uint256"}],"name":"uri","outputs":[{"internalType":"string","name":"","type":"string"}],"stateMutability":"view","type":"function"}]`
	bin = `60806040523480156200001157600080fd5b506040805160208101909152600081526200002c8162000033565b506200012f565b8051620000489060029060208401906200004c565b5050565b8280546200005a90620000f2565b90600052602060002090601f0160209004810192826200007e5760008555620000c9565b82601f106200009957805160ff1916838001178555620000c9565b82800160010185558215620000c9579182015b82811115620000c9578251825591602001919060010190620000ac565b50620000d7929150620000db565b5090565b5b80821115620000d75760008155600101620000dc565b600181811c908216806200010757607f821691505b602082108114156200012957634e487b7160e01b600052602260045260246000fd5b50919050565b612393806200013f6000396000f3fe608060405234801561001057600080fd5b50600436106100e95760003560e01c80634cd7e5381161008c578063a22cb46511610066578063a22cb465146101d6578063beabacc8146101e9578063e985e9c5146101fc578063f242432a1461024557600080fd5b80634cd7e538146101a55780634e1273f4146101ae57806377aa72c0146101ce57600080fd5b80632eb2c2d6116100c85780632eb2c2d6146101575780633593cebc1461016c57806340c10f191461017f5780634684d7e91461019257600080fd5b8062fdd58e146100ee57806301ffc9a7146101145780630e89341c14610137575b600080fd5b6101016100fc366004611e07565b610258565b6040519081526020015b60405180910390f35b610127610122366004611ef1565b610335565b604051901515815260200161010b565b61014a610145366004611f30565b61041a565b60405161010b91906120e7565b61016a610165366004611c3d565b6104ae565b005b61016a61017a366004611be1565b610577565b61016a61018d366004611e07565b61066b565b61016a6101a0366004611d81565b6106a0565b61010160035481565b6101c16101bc366004611e30565b6107a8565b60405161010b91906120a6565b600354610101565b61016a6101e4366004611dcd565b61099c565b61016a6101f7366004611ce3565b6109a7565b61012761020a366004611baf565b73ffffffffffffffffffffffffffffffffffffffff918216600090815260016020908152604080832093909416825291909152205460ff1690565b61016a610253366004611d1e565b6109c0565b600073ffffffffffffffffffffffffffffffffffffffff8316610302576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602b60248201527f455243313135353a2062616c616e636520717565727920666f7220746865207a60448201527f65726f206164647265737300000000000000000000000000000000000000000060648201526084015b60405180910390fd5b5060009081526020818152604080832073ffffffffffffffffffffffffffffffffffffffff949094168352929052205490565b60007fffffffff0000000000000000000000000000000000000000000000000000000082167fd9b67a260000000000000000000000000000000000000000000000000000000014806103c857507fffffffff0000000000000000000000000000000000000000000000000000000082167f0e89341c00000000000000000000000000000000000000000000000000000000145b8061041457507f01ffc9a7000000000000000000000000000000000000000000000000000000007fffffffff000000000000000000000000000000000000000000000000000000008316145b92915050565b60606002805461042990612136565b80601f016020809104026020016040519081016040528092919081815260200182805461045590612136565b80156104a25780601f10610477576101008083540402835291602001916104a2565b820191906000526020600020905b81548152906001019060200180831161048557829003601f168201915b50505050509050919050565b73ffffffffffffffffffffffffffffffffffffffff85163314806104d757506104d7853361020a565b610563576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152603260248201527f455243313135353a207472616e736665722063616c6c6572206973206e6f742060448201527f6f776e6572206e6f7220617070726f766564000000000000000000000000000060648201526084016102f9565b6105708585858585610a82565b5050505050565b6000815167ffffffffffffffff8111156105ba577f4e487b7100000000000000000000000000000000000000000000000000000000600052604160045260246000fd5b6040519080825280602002602001820160405280156105e3578160200160208202803683370190505b50905060005b825181101561064857600182828151811061062d577f4e487b7100000000000000000000000000000000000000000000000000000000600052603260045260246000fd5b6020908102919091010152610641816121d5565b90506105e9565b5061066584848484604051806020016040528060008152506104ae565b50505050565b6003805490600061067b836121d5565b919050555061069c8282600160405180602001604052806000815250610e0a565b5050565b6000815167ffffffffffffffff8111156106e3577f4e487b7100000000000000000000000000000000000000000000000000000000600052604160045260246000fd5b60405190808252806020026020018201604052801561070c578160200160208202803683370190505b50905060005b8251811015610787576001828281518110610756577f4e487b7100000000000000000000000000000000000000000000000000000000600052603260045260246000fd5b602090810291909101015260038054906000610771836121d5565b919050555080610780906121d5565b9050610712565b506107a383838360405180602001604052806000815250610f71565b505050565b6060815183511461083b576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602960248201527f455243313135353a206163636f756e747320616e6420696473206c656e67746860448201527f206d69736d61746368000000000000000000000000000000000000000000000060648201526084016102f9565b6000835167ffffffffffffffff81111561087e577f4e487b7100000000000000000000000000000000000000000000000000000000600052604160045260246000fd5b6040519080825280602002602001820160405280156108a7578160200160208202803683370190505b50905060005b8451811015610994576109408582815181106108f2577f4e487b7100000000000000000000000000000000000000000000000000000000600052603260045260246000fd5b6020026020010151858381518110610933577f4e487b7100000000000000000000000000000000000000000000000000000000600052603260045260246000fd5b6020026020010151610258565b828281518110610979577f4e487b7100000000000000000000000000000000000000000000000000000000600052603260045260246000fd5b602090810291909101015261098d816121d5565b90506108ad565b509392505050565b61069c338383611238565b6107a38383836001604051806020016040528060008152505b73ffffffffffffffffffffffffffffffffffffffff85163314806109e957506109e9853361020a565b610a75576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602960248201527f455243313135353a2063616c6c6572206973206e6f74206f776e6572206e6f7260448201527f20617070726f766564000000000000000000000000000000000000000000000060648201526084016102f9565b610570858585858561138c565b8151835114610b13576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602860248201527f455243313135353a2069647320616e6420616d6f756e7473206c656e6774682060448201527f6d69736d6174636800000000000000000000000000000000000000000000000060648201526084016102f9565b73ffffffffffffffffffffffffffffffffffffffff8416610bb6576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602560248201527f455243313135353a207472616e7366657220746f20746865207a65726f20616460448201527f647265737300000000000000000000000000000000000000000000000000000060648201526084016102f9565b3360005b8451811015610d75576000858281518110610bfe577f4e487b7100000000000000000000000000000000000000000000000000000000600052603260045260246000fd5b602002602001015190506000858381518110610c43577f4e487b7100000000000000000000000000000000000000000000000000000000600052603260045260246000fd5b6020908102919091018101516000848152808352604080822073ffffffffffffffffffffffffffffffffffffffff8e168352909352919091205490915081811015610d10576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602a60248201527f455243313135353a20696e73756666696369656e742062616c616e636520666f60448201527f72207472616e736665720000000000000000000000000000000000000000000060648201526084016102f9565b60008381526020818152604080832073ffffffffffffffffffffffffffffffffffffffff8e8116855292528083208585039055908b16825281208054849290610d5a90849061211e565b9250508190555050505080610d6e906121d5565b9050610bba565b508473ffffffffffffffffffffffffffffffffffffffff168673ffffffffffffffffffffffffffffffffffffffff168273ffffffffffffffffffffffffffffffffffffffff167f4a39dc06d4c0dbc64b70af90fd698a233a518aa5d07e595d983b8c0526c8f7fb8787604051610dec9291906120b9565b60405180910390a4610e028187878787876115bd565b505050505050565b73ffffffffffffffffffffffffffffffffffffffff8416610ead576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602160248201527f455243313135353a206d696e7420746f20746865207a65726f2061646472657360448201527f730000000000000000000000000000000000000000000000000000000000000060648201526084016102f9565b33610ec781600087610ebe88611857565b61057088611857565b60008481526020818152604080832073ffffffffffffffffffffffffffffffffffffffff8916845290915281208054859290610f0490849061211e565b9091555050604080518581526020810185905273ffffffffffffffffffffffffffffffffffffffff80881692600092918516917fc3d58168c5ae7397731d063d5bbf3d657854427343f4c083240f7aacaa2d0f62910160405180910390a4610570816000878787876118c9565b73ffffffffffffffffffffffffffffffffffffffff8416611014576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602160248201527f455243313135353a206d696e7420746f20746865207a65726f2061646472657360448201527f730000000000000000000000000000000000000000000000000000000000000060648201526084016102f9565b81518351146110a5576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602860248201527f455243313135353a2069647320616e6420616d6f756e7473206c656e6774682060448201527f6d69736d6174636800000000000000000000000000000000000000000000000060648201526084016102f9565b3360005b84518110156111a9578381815181106110eb577f4e487b7100000000000000000000000000000000000000000000000000000000600052603260045260246000fd5b602002602001015160008087848151811061112f577f4e487b7100000000000000000000000000000000000000000000000000000000600052603260045260246000fd5b6020026020010151815260200190815260200160002060008873ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff1681526020019081526020016000206000828254611191919061211e565b909155508190506111a1816121d5565b9150506110a9565b508473ffffffffffffffffffffffffffffffffffffffff16600073ffffffffffffffffffffffffffffffffffffffff168273ffffffffffffffffffffffffffffffffffffffff167f4a39dc06d4c0dbc64b70af90fd698a233a518aa5d07e595d983b8c0526c8f7fb87876040516112219291906120b9565b60405180910390a4610570816000878787876115bd565b8173ffffffffffffffffffffffffffffffffffffffff168373ffffffffffffffffffffffffffffffffffffffff1614156112f4576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602960248201527f455243313135353a2073657474696e6720617070726f76616c2073746174757360448201527f20666f722073656c66000000000000000000000000000000000000000000000060648201526084016102f9565b73ffffffffffffffffffffffffffffffffffffffff83811660008181526001602090815260408083209487168084529482529182902080547fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff001686151590811790915591519182527f17307eab39ab6107e8899845ad3d59bd9653f200f220920489ca2b5937696c31910160405180910390a3505050565b73ffffffffffffffffffffffffffffffffffffffff841661142f576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602560248201527f455243313135353a207472616e7366657220746f20746865207a65726f20616460448201527f647265737300000000000000000000000000000000000000000000000000000060648201526084016102f9565b3361143f818787610ebe88611857565b60008481526020818152604080832073ffffffffffffffffffffffffffffffffffffffff8a168452909152902054838110156114fd576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602a60248201527f455243313135353a20696e73756666696369656e742062616c616e636520666f60448201527f72207472616e736665720000000000000000000000000000000000000000000060648201526084016102f9565b60008581526020818152604080832073ffffffffffffffffffffffffffffffffffffffff8b811685529252808320878503905590881682528120805486929061154790849061211e565b9091555050604080518681526020810186905273ffffffffffffffffffffffffffffffffffffffff808916928a821692918616917fc3d58168c5ae7397731d063d5bbf3d657854427343f4c083240f7aacaa2d0f62910160405180910390a46115b48288888888886118c9565b50505050505050565b73ffffffffffffffffffffffffffffffffffffffff84163b15610e02576040517fbc197c8100000000000000000000000000000000000000000000000000000000815273ffffffffffffffffffffffffffffffffffffffff85169063bc197c81906116349089908990889088908890600401611feb565b602060405180830381600087803b15801561164e57600080fd5b505af192505050801561169c575060408051601f3d9081017fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe016820190925261169991810190611f14565b60015b611786576116a861226c565b806308c379a014156116fc57506116bd612284565b806116c857506116fe565b806040517f08c379a00000000000000000000000000000000000000000000000000000000081526004016102f991906120e7565b505b6040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152603460248201527f455243313135353a207472616e7366657220746f206e6f6e204552433131353560448201527f526563656976657220696d706c656d656e74657200000000000000000000000060648201526084016102f9565b7fffffffff0000000000000000000000000000000000000000000000000000000081167fbc197c8100000000000000000000000000000000000000000000000000000000146115b4576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602860248201527f455243313135353a204552433131353552656365697665722072656a6563746560448201527f6420746f6b656e7300000000000000000000000000000000000000000000000060648201526084016102f9565b604080516001808252818301909252606091600091906020808301908036833701905050905082816000815181106118b8577f4e487b7100000000000000000000000000000000000000000000000000000000600052603260045260246000fd5b602090810291909101015292915050565b73ffffffffffffffffffffffffffffffffffffffff84163b15610e02576040517ff23a6e6100000000000000000000000000000000000000000000000000000000815273ffffffffffffffffffffffffffffffffffffffff85169063f23a6e61906119409089908990889088908890600401612056565b602060405180830381600087803b15801561195a57600080fd5b505af19250505080156119a8575060408051601f3d9081017fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe01682019092526119a591810190611f14565b60015b6119b4576116a861226c565b7fffffffff0000000000000000000000000000000000000000000000000000000081167ff23a6e6100000000000000000000000000000000000000000000000000000000146115b4576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602860248201527f455243313135353a204552433131353552656365697665722072656a6563746560448201527f6420746f6b656e7300000000000000000000000000000000000000000000000060648201526084016102f9565b803573ffffffffffffffffffffffffffffffffffffffff81168114611aa957600080fd5b919050565b600082601f830112611abe578081fd5b81356020611acb826120fa565b604051611ad8828261218a565b8381528281019150858301600585901b87018401881015611af7578586fd5b855b85811015611b1557813584529284019290840190600101611af9565b5090979650505050505050565b600082601f830112611b32578081fd5b813567ffffffffffffffff811115611b4c57611b4c61223d565b604051611b8160207fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe0601f850116018261218a565b818152846020838601011115611b95578283fd5b816020850160208301379081016020019190915292915050565b60008060408385031215611bc1578182fd5b611bca83611a85565b9150611bd860208401611a85565b90509250929050565b600080600060608486031215611bf5578081fd5b611bfe84611a85565b9250611c0c60208501611a85565b9150604084013567ffffffffffffffff811115611c27578182fd5b611c3386828701611aae565b9150509250925092565b600080600080600060a08688031215611c54578081fd5b611c5d86611a85565b9450611c6b60208701611a85565b9350604086013567ffffffffffffffff80821115611c87578283fd5b611c9389838a01611aae565b94506060880135915080821115611ca8578283fd5b611cb489838a01611aae565b93506080880135915080821115611cc9578283fd5b50611cd688828901611b22565b9150509295509295909350565b600080600060608486031215611cf7578283fd5b611d0084611a85565b9250611d0e60208501611a85565b9150604084013590509250925092565b600080600080600060a08688031215611d35578081fd5b611d3e86611a85565b9450611d4c60208701611a85565b93506040860135925060608601359150608086013567ffffffffffffffff811115611d75578182fd5b611cd688828901611b22565b60008060408385031215611d93578182fd5b611d9c83611a85565b9150602083013567ffffffffffffffff811115611db7578182fd5b611dc385828601611aae565b9150509250929050565b60008060408385031215611ddf578182fd5b611de883611a85565b915060208301358015158114611dfc578182fd5b809150509250929050565b60008060408385031215611e19578182fd5b611e2283611a85565b946020939093013593505050565b60008060408385031215611e42578081fd5b823567ffffffffffffffff80821115611e59578283fd5b818501915085601f830112611e6c578283fd5b81356020611e79826120fa565b604051611e86828261218a565b8381528281019150858301600585901b870184018b1015611ea5578788fd5b8796505b84871015611ece57611eba81611a85565b835260019690960195918301918301611ea9565b5096505086013592505080821115611ee4578283fd5b50611dc385828601611aae565b600060208284031215611f02578081fd5b8135611f0d8161232c565b9392505050565b600060208284031215611f25578081fd5b8151611f0d8161232c565b600060208284031215611f41578081fd5b5035919050565b6000815180845260208085019450808401835b83811015611f7757815187529582019590820190600101611f5b565b509495945050505050565b60008151808452815b81811015611fa757602081850181015186830182015201611f8b565b81811115611fb85782602083870101525b50601f017fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe0169290920160200192915050565b600073ffffffffffffffffffffffffffffffffffffffff808816835280871660208401525060a0604083015261202460a0830186611f48565b82810360608401526120368186611f48565b9050828103608084015261204a8185611f82565b98975050505050505050565b600073ffffffffffffffffffffffffffffffffffffffff808816835280871660208401525084604083015283606083015260a0608083015261209b60a0830184611f82565b979650505050505050565b602081526000611f0d6020830184611f48565b6040815260006120cc6040830185611f48565b82810360208401526120de8185611f48565b95945050505050565b602081526000611f0d6020830184611f82565b600067ffffffffffffffff8211156121145761211461223d565b5060051b60200190565b600082198211156121315761213161220e565b500190565b600181811c9082168061214a57607f821691505b60208210811415612184577f4e487b7100000000000000000000000000000000000000000000000000000000600052602260045260246000fd5b50919050565b7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe0601f830116810181811067ffffffffffffffff821117156121ce576121ce61223d565b6040525050565b60007fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff8214156122075761220761220e565b5060010190565b7f4e487b7100000000000000000000000000000000000000000000000000000000600052601160045260246000fd5b7f4e487b7100000000000000000000000000000000000000000000000000000000600052604160045260246000fd5b600060033d111561228157600481823e5160e01c5b90565b600060443d10156122925790565b6040517ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffc803d016004833e81513d67ffffffffffffffff81602484011181841117156122e057505050505090565b82850191508151818111156122f85750505050505090565b843d87010160208285010111156123125750505050505090565b6123216020828601018761218a565b509095945050505050565b7fffffffff000000000000000000000000000000000000000000000000000000008116811461235a57600080fd5b5056fea2646970667358221220a6a923af8cea5d4642912437c54fc3d9f3215c996e0412c19fb639d206de031d64736f6c63430008040033`
)
