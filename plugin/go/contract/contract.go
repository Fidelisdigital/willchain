package contract

import (
	"bytes"
	"encoding/binary"
	"log"
	"math/rand"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
)

/* This file contains the base contract implementation that overrides the basic 'transfer' functionality */

var ContractConfig = &PluginConfig{
	Name:    "go_plugin_contract",
	Id:      1,
	Version: 1,
	SupportedTransactions: []string{
		"send",
		"create_will",
		"reset_timer",
		"claim_will",
		"cancel_will",
	},
	TransactionTypeUrls: []string{
		"type.googleapis.com/types.MessageSend",
		"type.googleapis.com/types.MessageCreateWill",
		"type.googleapis.com/types.MessageResetTimer",
		"type.googleapis.com/types.MessageClaimWill",
		"type.googleapis.com/types.MessageCancelWill",
	},
	EventTypeUrls: nil,
}

func init() {
	file_account_proto_init()
	file_event_proto_init()
	file_plugin_proto_init()
	file_tx_proto_init()
	var fds [][]byte
	for _, file := range []protoreflect.FileDescriptor{
		anypb.File_google_protobuf_any_proto,
		File_account_proto, File_event_proto, File_plugin_proto, File_tx_proto,
	} {
		fd, _ := proto.Marshal(protodesc.ToFileDescriptorProto(file))
		fds = append(fds, fd)
	}
	ContractConfig.FileDescriptorProtos = fds
}

type Contract struct {
	Config        Config
	FSMConfig     *PluginFSMConfig
	plugin        *Plugin
	fsmId         uint64
	currentHeight uint64
}

func (c *Contract) Genesis(_ *PluginGenesisRequest) *PluginGenesisResponse {
	return &PluginGenesisResponse{}
}

func (c *Contract) BeginBlock(request *PluginBeginRequest) *PluginBeginResponse {
	c.currentHeight = request.Height
	return &PluginBeginResponse{}
}

func (c *Contract) CheckTx(request *PluginCheckRequest) *PluginCheckResponse {
	resp, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: rand.Uint64(), Key: KeyForFeeParams()},
		}})
	if err == nil {
		err = resp.Error
	}
	if err != nil {
		return &PluginCheckResponse{Error: err}
	}
	minFees := new(FeeParams)
	if err = Unmarshal(resp.Results[0].Entries[0].Value, minFees); err != nil {
		return &PluginCheckResponse{Error: err}
	}
	if request.Tx.Fee < minFees.SendFee {
		return &PluginCheckResponse{Error: ErrTxFeeBelowStateLimit()}
	}
	msg, err := FromAny(request.Tx.Msg)
	if err != nil {
		return &PluginCheckResponse{Error: err}
	}
	switch x := msg.(type) {
	case *MessageSend:
		return c.CheckMessageSend(x)
	case *MessageCreateWill:
		return c.CheckMessageCreateWill(x)
	case *MessageResetTimer:
		return c.CheckMessageResetTimer(x)
	case *MessageClaimWill:
		return c.CheckMessageClaimWill(x)
	case *MessageCancelWill:
		return c.CheckMessageCancelWill(x)
	default:
		return &PluginCheckResponse{Error: ErrInvalidMessageCast()}
	}
}

func (c *Contract) DeliverTx(request *PluginDeliverRequest) *PluginDeliverResponse {
	msg, err := FromAny(request.Tx.Msg)
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	switch x := msg.(type) {
	case *MessageSend:
		return c.DeliverMessageSend(x, request.Tx.Fee)
	case *MessageCreateWill:
		return c.DeliverMessageCreateWill(x, request.Tx.Fee)
	case *MessageResetTimer:
		return c.DeliverMessageResetTimer(x, request.Tx.Fee)
	case *MessageClaimWill:
		return c.DeliverMessageClaimWill(x, request.Tx.Fee)
	case *MessageCancelWill:
		return c.DeliverMessageCancelWill(x, request.Tx.Fee)
	default:
		return &PluginDeliverResponse{Error: ErrInvalidMessageCast()}
	}
}

func (c *Contract) EndBlock(_ *PluginEndRequest) *PluginEndResponse {
	return &PluginEndResponse{}
}

func (c *Contract) CheckMessageSend(msg *MessageSend) *PluginCheckResponse {
	if len(msg.FromAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if len(msg.ToAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if msg.Amount == 0 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	return &PluginCheckResponse{Recipient: msg.ToAddress, AuthorizedSigners: [][]byte{msg.FromAddress}}
}

func (c *Contract) DeliverMessageSend(msg *MessageSend, fee uint64) *PluginDeliverResponse {
	log.Printf("DeliverMessageSend called: from=%x to=%x amount=%d fee=%d", msg.FromAddress, msg.ToAddress, msg.Amount, fee)
	var (
		fromKey, toKey, feePoolKey         []byte
		fromBytes, toBytes, feePoolBytes   []byte
		fromQueryId, toQueryId, feeQueryId = rand.Uint64(), rand.Uint64(), rand.Uint64()
		from, to, feePool                  = new(Account), new(Account), new(Pool)
	)
	fromKey, toKey, feePoolKey = KeyForAccount(msg.FromAddress), KeyForAccount(msg.ToAddress), KeyForFeePool(c.Config.ChainId)
	response, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: feeQueryId, Key: feePoolKey},
			{QueryId: fromQueryId, Key: fromKey},
			{QueryId: toQueryId, Key: toKey},
		}})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if response.Error != nil {
		return &PluginDeliverResponse{Error: response.Error}
	}
	for _, resp := range response.Results {
		if len(resp.Entries) == 0 {
			continue
		}
		switch resp.QueryId {
		case fromQueryId:
			fromBytes = resp.Entries[0].Value
		case toQueryId:
			toBytes = resp.Entries[0].Value
		case feeQueryId:
			feePoolBytes = resp.Entries[0].Value
		}
	}
	amountToDeduct := msg.Amount + fee
	if err = Unmarshal(fromBytes, from); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if err = Unmarshal(toBytes, to); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if err = Unmarshal(feePoolBytes, feePool); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if from.Amount < amountToDeduct {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}
	if bytes.Equal(fromKey, toKey) {
		to = from
	}
	from.Amount -= amountToDeduct
	feePool.Amount += fee
	to.Amount += msg.Amount
	fromBytes, err = Marshal(from)
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	toBytes, err = Marshal(to)
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	feePoolBytes, err = Marshal(feePool)
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	var resp *PluginStateWriteResponse
	if from.Amount == 0 {
		resp, err = c.plugin.StateWrite(c, &PluginStateWriteRequest{
			Sets: []*PluginSetOp{
				{Key: feePoolKey, Value: feePoolBytes},
				{Key: toKey, Value: toBytes},
			},
			Deletes: []*PluginDeleteOp{{Key: fromKey}},
		})
	} else {
		resp, err = c.plugin.StateWrite(c, &PluginStateWriteRequest{
			Sets: []*PluginSetOp{
				{Key: feePoolKey, Value: feePoolBytes},
				{Key: toKey, Value: toBytes},
				{Key: fromKey, Value: fromBytes},
			},
		})
	}
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if resp.Error != nil {
		return &PluginDeliverResponse{Error: resp.Error}
	}
	return &PluginDeliverResponse{}
}

var (
	accountPrefix     = []byte{1}
	poolPrefix        = []byte{2}
	paramsPrefix      = []byte{7}
	willPrefix        = []byte{3}
	willCounterPrefix = []byte{4}
)

func KeyForAccount(addr []byte) []byte {
	return JoinLenPrefix(accountPrefix, addr)
}
func KeyForFeeParams() []byte {
	return JoinLenPrefix(paramsPrefix, []byte("/f/"))
}
func KeyForFeePool(chainId uint64) []byte {
	return JoinLenPrefix(poolPrefix, formatUint64(chainId))
}
func KeyForWill(id uint64) []byte {
	return JoinLenPrefix(willPrefix, formatUint64(id))
}
func KeyForWillCounter() []byte {
	return JoinLenPrefix(willCounterPrefix, []byte("wc"))
}
func formatUint64(u uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, u)
	return b
}

func (c *Contract) CheckMessageCreateWill(msg *MessageCreateWill) *PluginCheckResponse {
	if len(msg.OwnerAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if len(msg.BeneficiaryAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if msg.Amount == 0 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	if msg.ClaimAfterBlocks == 0 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	if len(msg.Message) > 280 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	return &PluginCheckResponse{AuthorizedSigners: [][]byte{msg.OwnerAddress}}
}

func (c *Contract) CheckMessageResetTimer(msg *MessageResetTimer) *PluginCheckResponse {
	if len(msg.OwnerAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if msg.WillId == 0 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	return &PluginCheckResponse{AuthorizedSigners: [][]byte{msg.OwnerAddress}}
}

func (c *Contract) CheckMessageClaimWill(msg *MessageClaimWill) *PluginCheckResponse {
	if len(msg.BeneficiaryAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if msg.WillId == 0 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	return &PluginCheckResponse{AuthorizedSigners: [][]byte{msg.BeneficiaryAddress}}
}

func (c *Contract) CheckMessageCancelWill(msg *MessageCancelWill) *PluginCheckResponse {
	if len(msg.OwnerAddress) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	if msg.WillId == 0 {
		return &PluginCheckResponse{Error: ErrInvalidAmount()}
	}
	return &PluginCheckResponse{AuthorizedSigners: [][]byte{msg.OwnerAddress}}
}

func (c *Contract) DeliverMessageCreateWill(msg *MessageCreateWill, fee uint64) *PluginDeliverResponse {
	var (
		ownerQueryId, counterQueryId, feeQueryId = rand.Uint64(), rand.Uint64(), rand.Uint64()
		ownerBytes, counterBytes, feePoolBytes   []byte
		owner, counter, feePool                  = new(Account), new(WillCounter), new(Pool)
	)
	response, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: ownerQueryId, Key: KeyForAccount(msg.OwnerAddress)},
			{QueryId: counterQueryId, Key: KeyForWillCounter()},
			{QueryId: feeQueryId, Key: KeyForFeePool(c.Config.ChainId)},
		}})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if response.Error != nil {
		return &PluginDeliverResponse{Error: response.Error}
	}
	for _, resp := range response.Results {
		if len(resp.Entries) == 0 {
			continue
		}
		switch resp.QueryId {
		case ownerQueryId:
			ownerBytes = resp.Entries[0].Value
		case counterQueryId:
			counterBytes = resp.Entries[0].Value
		case feeQueryId:
			feePoolBytes = resp.Entries[0].Value
		}
	}
	if err = Unmarshal(ownerBytes, owner); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	Unmarshal(counterBytes, counter)
	if err = Unmarshal(feePoolBytes, feePool); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	amountToDeduct := msg.Amount + fee
	if owner.Amount < amountToDeduct {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}
	counter.Count++
	will := &Will{
		Id:                 counter.Count,
		OwnerAddress:       msg.OwnerAddress,
		BeneficiaryAddress: msg.BeneficiaryAddress,
		Amount:             msg.Amount,
		ClaimAfterHeight:   c.currentHeight + msg.ClaimAfterBlocks,
		OriginalBlocks:     msg.ClaimAfterBlocks,
		Message:            msg.Message,
		CreatedHeight:      c.currentHeight,
		IsActive:           true,
	}
	owner.Amount -= amountToDeduct
	feePool.Amount += fee
	ownerBytes, _ = Marshal(owner)
	counterBytes, _ = Marshal(counter)
	feePoolBytes, _ = Marshal(feePool)
	willBytes, _ := Marshal(will)
	resp, err := c.plugin.StateWrite(c, &PluginStateWriteRequest{
		Sets: []*PluginSetOp{
			{Key: KeyForWill(will.Id), Value: willBytes},
			{Key: KeyForWillCounter(), Value: counterBytes},
			{Key: KeyForAccount(msg.OwnerAddress), Value: ownerBytes},
			{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBytes},
		},
	})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if resp.Error != nil {
		return &PluginDeliverResponse{Error: resp.Error}
	}
	log.Printf("Created Will %d for owner %x", will.Id, msg.OwnerAddress)
	return &PluginDeliverResponse{}
}

func (c *Contract) DeliverMessageResetTimer(msg *MessageResetTimer, fee uint64) *PluginDeliverResponse {
	var (
		willQueryId, ownerQueryId, feeQueryId = rand.Uint64(), rand.Uint64(), rand.Uint64()
		willBytes, ownerBytes, feePoolBytes   []byte
		will, owner, feePool                  = new(Will), new(Account), new(Pool)
	)
	response, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: willQueryId, Key: KeyForWill(msg.WillId)},
			{QueryId: ownerQueryId, Key: KeyForAccount(msg.OwnerAddress)},
			{QueryId: feeQueryId, Key: KeyForFeePool(c.Config.ChainId)},
		}})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if response.Error != nil {
		return &PluginDeliverResponse{Error: response.Error}
	}
	for _, resp := range response.Results {
		if len(resp.Entries) == 0 {
			continue
		}
		switch resp.QueryId {
		case willQueryId:
			willBytes = resp.Entries[0].Value
		case ownerQueryId:
			ownerBytes = resp.Entries[0].Value
		case feeQueryId:
			feePoolBytes = resp.Entries[0].Value
		}
	}
	if len(willBytes) == 0 {
		return &PluginDeliverResponse{Error: NewError(15, DefaultModule, "will not found")}
	}
	if err = Unmarshal(willBytes, will); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if !bytes.Equal(will.OwnerAddress, msg.OwnerAddress) {
		return &PluginDeliverResponse{Error: NewError(16, DefaultModule, "not the owner")}
	}
	if !will.IsActive {
		return &PluginDeliverResponse{Error: NewError(17, DefaultModule, "will is not active")}
	}
	if err = Unmarshal(ownerBytes, owner); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if owner.Amount < fee {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}
	if err = Unmarshal(feePoolBytes, feePool); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	will.ClaimAfterHeight = c.currentHeight + will.OriginalBlocks
	owner.Amount -= fee
	feePool.Amount += fee
	willBytes, _ = Marshal(will)
	ownerBytes, _ = Marshal(owner)
	feePoolBytes, _ = Marshal(feePool)
	resp, err := c.plugin.StateWrite(c, &PluginStateWriteRequest{
		Sets: []*PluginSetOp{
			{Key: KeyForWill(will.Id), Value: willBytes},
			{Key: KeyForAccount(msg.OwnerAddress), Value: ownerBytes},
			{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBytes},
		},
	})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if resp.Error != nil {
		return &PluginDeliverResponse{Error: resp.Error}
	}
	log.Printf("Reset timer for Will %d", will.Id)
	return &PluginDeliverResponse{}
}

func (c *Contract) DeliverMessageClaimWill(msg *MessageClaimWill, fee uint64) *PluginDeliverResponse {
	var (
		willQueryId, benefQueryId, feeQueryId = rand.Uint64(), rand.Uint64(), rand.Uint64()
		willBytes, benefBytes, feePoolBytes   []byte
		will, beneficiary, feePool            = new(Will), new(Account), new(Pool)
	)
	response, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: willQueryId, Key: KeyForWill(msg.WillId)},
			{QueryId: benefQueryId, Key: KeyForAccount(msg.BeneficiaryAddress)},
			{QueryId: feeQueryId, Key: KeyForFeePool(c.Config.ChainId)},
		}})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if response.Error != nil {
		return &PluginDeliverResponse{Error: response.Error}
	}
	for _, resp := range response.Results {
		if len(resp.Entries) == 0 {
			continue
		}
		switch resp.QueryId {
		case willQueryId:
			willBytes = resp.Entries[0].Value
		case benefQueryId:
			benefBytes = resp.Entries[0].Value
		case feeQueryId:
			feePoolBytes = resp.Entries[0].Value
		}
	}
	if len(willBytes) == 0 {
		return &PluginDeliverResponse{Error: NewError(15, DefaultModule, "will not found")}
	}
	if err = Unmarshal(willBytes, will); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if !bytes.Equal(will.BeneficiaryAddress, msg.BeneficiaryAddress) {
		return &PluginDeliverResponse{Error: NewError(18, DefaultModule, "not the beneficiary")}
	}
	if !will.IsActive {
		return &PluginDeliverResponse{Error: NewError(17, DefaultModule, "will is not active")}
	}
	if c.currentHeight <= will.ClaimAfterHeight {
		return &PluginDeliverResponse{Error: NewError(19, DefaultModule, "will not yet claimable")}
	}
	if err = Unmarshal(benefBytes, beneficiary); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if beneficiary.Amount < fee {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}
	if err = Unmarshal(feePoolBytes, feePool); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	beneficiary.Amount += will.Amount
	beneficiary.Amount -= fee
	feePool.Amount += fee
	will.IsActive = false
	willBytes, _ = Marshal(will)
	benefBytes, _ = Marshal(beneficiary)
	feePoolBytes, _ = Marshal(feePool)
	resp, err := c.plugin.StateWrite(c, &PluginStateWriteRequest{
		Sets: []*PluginSetOp{
			{Key: KeyForWill(will.Id), Value: willBytes},
			{Key: KeyForAccount(msg.BeneficiaryAddress), Value: benefBytes},
			{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBytes},
		},
	})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if resp.Error != nil {
		return &PluginDeliverResponse{Error: resp.Error}
	}
	log.Printf("Claimed Will %d by %x", will.Id, msg.BeneficiaryAddress)
	return &PluginDeliverResponse{}
}

func (c *Contract) DeliverMessageCancelWill(msg *MessageCancelWill, fee uint64) *PluginDeliverResponse {
	var (
		willQueryId, ownerQueryId, feeQueryId = rand.Uint64(), rand.Uint64(), rand.Uint64()
		willBytes, ownerBytes, feePoolBytes   []byte
		will, owner, feePool                  = new(Will), new(Account), new(Pool)
	)
	response, err := c.plugin.StateRead(c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{
			{QueryId: willQueryId, Key: KeyForWill(msg.WillId)},
			{QueryId: ownerQueryId, Key: KeyForAccount(msg.OwnerAddress)},
			{QueryId: feeQueryId, Key: KeyForFeePool(c.Config.ChainId)},
		}})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if response.Error != nil {
		return &PluginDeliverResponse{Error: response.Error}
	}
	for _, resp := range response.Results {
		if len(resp.Entries) == 0 {
			continue
		}
		switch resp.QueryId {
		case willQueryId:
			willBytes = resp.Entries[0].Value
		case ownerQueryId:
			ownerBytes = resp.Entries[0].Value
		case feeQueryId:
			feePoolBytes = resp.Entries[0].Value
		}
	}
	if len(willBytes) == 0 {
		return &PluginDeliverResponse{Error: NewError(15, DefaultModule, "will not found")}
	}
	if err = Unmarshal(willBytes, will); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if !bytes.Equal(will.OwnerAddress, msg.OwnerAddress) {
		return &PluginDeliverResponse{Error: NewError(16, DefaultModule, "not the owner")}
	}
	if !will.IsActive {
		return &PluginDeliverResponse{Error: NewError(17, DefaultModule, "will is not active")}
	}
	if err = Unmarshal(ownerBytes, owner); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if owner.Amount < fee {
		return &PluginDeliverResponse{Error: ErrInsufficientFunds()}
	}
	if err = Unmarshal(feePoolBytes, feePool); err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	owner.Amount += will.Amount
	owner.Amount -= fee
	feePool.Amount += fee
	will.IsActive = false
	willBytes, _ = Marshal(will)
	ownerBytes, _ = Marshal(owner)
	feePoolBytes, _ = Marshal(feePool)
	resp, err := c.plugin.StateWrite(c, &PluginStateWriteRequest{
		Sets: []*PluginSetOp{
			{Key: KeyForWill(will.Id), Value: willBytes},
			{Key: KeyForAccount(msg.OwnerAddress), Value: ownerBytes},
			{Key: KeyForFeePool(c.Config.ChainId), Value: feePoolBytes},
		},
	})
	if err != nil {
		return &PluginDeliverResponse{Error: err}
	}
	if resp.Error != nil {
		return &PluginDeliverResponse{Error: resp.Error}
	}
	log.Printf("Cancelled Will %d by owner %x", will.Id, msg.OwnerAddress)
	return &PluginDeliverResponse{}
}