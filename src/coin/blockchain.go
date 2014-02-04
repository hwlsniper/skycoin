package coin

import (
    "errors"
    "github.com/op/go-logging"
    "github.com/skycoin/encoder"
    "log"
    //"sort"
    "time"
)

var (
    logger = logging.MustGetLogger("skycoin.coin")
)

//Note: a droplet is the base coin unit. Each Skycoin is one million droplets

//TODO: more abstract struct names
// /s/UxOut/Ux ?
// /s/Transaction/Tx ?

//TODO:
// HashArray - array of hashes
// TxArray - array of Tx/transactions
// UxArray - array of Ux/outputs
// Blockchain.txUxIn(tx *Tx) ([]Ux, error)  - inputs of transaction

//Termonology:
// UXTO - unspent transaction outputs
// UX - outputs
// TX - transactions

//Notes:
// transactions (TX) consume outputs (UX) and produce new outputs (UX)
// Tx.Uxi() - set of outputs consumed by transaction
// Tx.Uxo() - set of outputs created by transaction

const (
    // If the block header time is further in the future than this, it is
    // rejected.
    blockTimeFutureMultipleMax uint64 = 20
    genesisCoinVolume          uint64 = 100 * 1e6 * 1e6 //100 million coins
    genesisCoinHours           uint64 = 1024 * 1024 * 1024
    //each coin is one million droplets, which are the base unit
)

type Block struct {
    Header BlockHeader
    Body   BlockBody
}

type BlockHeader struct {
    Version uint32

    Time  uint64
    BkSeq uint64 //increment every block
    Fee   uint64 //fee in block, used for Proof of Stake

    PrevHash SHA256 //hash of header of previous block
    BodyHash SHA256 //hash of transaction block
}

type BlockBody struct {
    Transactions Transactions
}

//TODO: merge header/body and cleanup top level interface

/*
Todo: merge header/body

type Block struct {
    Time  uint64
    BkSeq uint64 //increment every block
    Fee   uint64 //fee in block, used for Proof of Stake

    HashPrevBlock SHA256 //hash of header of previous block
    BodyHash      SHA256 //hash of transaction block

    Transactions Transactions
}
*/

func newBlock(prev *Block, creationInterval uint64) Block {
    header := newBlockHeader(&prev.Header, creationInterval)
    return Block{Header: header, Body: BlockBody{}}
}

func (self *Block) HashHeader() SHA256 {
    return self.Header.Hash()
}

func (self *BlockHeader) Hash() SHA256 {
    b1 := encoder.Serialize(*self)
    return SumDoubleSHA256(b1)
}

//merkle hash of transactions in block
func (self *Block) HashBody() SHA256 {
    var hashes []SHA256
    for _, t := range self.Body.Transactions {
        hashes = append(hashes, t.Hash())
    }
    return Merkle(hashes) //merkle hash of transactions
}

func (self *Block) UpdateHeader() {
    self.Header.BodyHash = self.HashBody()
}

func newBlockHeader(prev *BlockHeader, creationInterval uint64) BlockHeader {
    return BlockHeader{
        // TODO -- what about the rest of the fields??
        PrevHash: prev.Hash(),
        Time:     prev.Time + creationInterval,
        BkSeq:    prev.BkSeq + 1,
    }
}

func (self *BlockHeader) Bytes() []byte {
    return encoder.Serialize(*self)
}

func (self *BlockBody) Bytes() []byte {
    return encoder.Serialize(*self)
}

type Blockchain struct {
    Blocks  []Block
    Unspent UnspentPool
    // How often new blocks are created
    CreationInterval uint64
    // Enable security checks for internal and 'impossible' errors
    Paranoid bool
}

func NewBlockchain(genesisAddress Address, creationInterval uint64) *Blockchain {
    logger.Debug("Creating new block chain with genesis %s",
        genesisAddress.String())
    var bc *Blockchain = &Blockchain{
        CreationInterval: creationInterval,
        Blocks:           make([]Block, 0),
        Unspent:          NewUnspentPool(),
        Paranoid:         true,
    }

    //set genesis block
    var b Block = Block{} // genesis block
    b.Header.Time = bc.TimeNow()
    bc.Blocks = append(bc.Blocks, b)
    // Genesis output
    ux := UxOut{
        Head: UxHead{
            Time:  b.Header.Time,
            BkSeq: 0,
        },
        Body: UxBody{
            SrcTransaction: SHA256{},
            Address:        genesisAddress,
            Coins:          genesisCoinVolume, // 100 million
            Hours:          genesisCoinHours,
        },
    }
    bc.Unspent.Add(ux)
    return bc
}

func (self *Blockchain) Head() *Block {
    return &self.Blocks[len(self.Blocks)-1]
}

//Time returns time of last block
//used as system clock indepedent clock for coin hour calculations
func (self *Blockchain) Time() uint64 {
    return self.Head().Header.Time
}

//TimeNow returns current system time
//TODO: use syncronized network time instead of system time
//TODO: add function pointer to external network time callback?
func (self *Blockchain) TimeNow() uint64 {
    return uint64(time.Now().UTC().Unix())
}

// Creates a Block given an array of Transactions.  It does not verify the
// block; ExecuteBlock will handle verification.  txns must be sorted by hash
func (self *Blockchain) NewBlockFromTransactions(txns Transactions) (Block, error) {
    b := newBlock(self.Head(), self.CreationInterval)
    newtxns := self.arbitrateTransactions(txns)
    b.Body.Transactions = newtxns
    b.UpdateHeader()
    return b, nil
}

/*
   Validation
*/

/*
   Validation
*/

// Returns an array of outputs a transaction would spend, An error is returned
// if the any unspents are not found.
func (self *Blockchain) txUxIn(tx *Transaction) (UxArray, error) {
    uxia := make(UxArray, len(tx.In))
    for i, txi := range tx.In {
        uxi, exists := self.Unspent.Get(txi.UxOut)
        if !exists {
            return nil, errors.New("txUxIn error, unspent output does not exist")
        }
        uxia[i] = uxi
    }
    return uxia, nil
}

// Validates the inputs to a transaction by checking signatures, duplicate
// inputs and double spends
func (self *Blockchain) txUxInChk(tx *Transaction, uxIn UxArray) error {
    // Check signatures
    for i, _ := range tx.In {
        ux := uxIn[i]
        err := ChkSig(ux.Body.Address, tx.Header.Hash, tx.Header.Sigs[i])
        if err != nil {
            return err
        }
    }
    // Check for duplicate inputs
    if uxIn.HasDupes() {
        return errors.New("txUxInChk error: duplicate inputs")
    }

    if self.Paranoid {
        // Check that hashes match.  This would imply a bug with txUxIn.
        for i, txi := range tx.In {
            if txi.UxOut != uxIn[i].Hash() {
                log.Panic("Ux hash mismatch")
            }
        }
        // Assert monotome time/coinhouse increase.
        for i, _ := range tx.In {
            if uxIn[i].CoinHours(self.Time()) < uxIn[i].Body.Hours {
                log.Panic("Uxi.CoinHours < uxi.Body.Hours")
            }
        }
    }

    return nil
}

//txUxOutChk validates the outputs that would be created by the transaction
//txUxOutChk checks for duplicate output hashes
//txUxOutChk checks for hash collisions with existing hashes
func (self *Blockchain) txUxOutChk(tx *Transaction, uxOut UxArray) error {
    // Check for outputs with duplicate hashes
    if uxOut.HasDupes() {
        return errors.New("Duplicate hash outputs")
    }
    //check for hash collisions of outputs with unspent output set
    hashes := uxOut.HashArray()
    for _, uxhash := range hashes {
        if _, exists := self.Unspent.Get(uxhash); exists {
            return errors.New("Output hash collision with unspent outputs")
        }
    }

    //check misc outputs conditions
    for _, ux := range uxOut {
        //disallow allow zero coin outputs
        if ux.Body.Coins == 0 {
            return errors.New("Zero coin output")
        }
        // each transaction output should multiple of 10e6 base units,
        // to prevent utxo spam
        if ux.Body.Coins%10e6 != 0 {
            return errors.New("Outputs must be multiple of 10e6 base units")
        }
    }

    return nil
}

// Checks for errors in relationship between the inputs and outputs of
// the transaction
func (self *Blockchain) txUxChk(tx *Transaction, uxIn UxArray,
    uxOut UxArray) error {
    var headTime uint64 = self.Time()
    var coinsIn uint64 = 0
    var hoursIn uint64 = 0
    for _, ux := range uxIn {
        coinsIn += ux.Body.Coins
        hoursIn += ux.CoinHours(headTime)
    }
    var coinsOut uint64 = 0
    var hoursOut uint64 = 0
    for _, ux := range uxOut {
        coinsOut += ux.Body.Coins
        hoursOut += ux.Body.Hours
    }
    if coinsIn != coinsOut {
        return errors.New("Transactions may not create or destroy coins")
    }
    if hoursIn < hoursOut {
        return errors.New("Insufficient coin hours for outputs")
    }
    return nil
}

// VerifyTransaction determines whether a transaction could be executed in the
// current block
// VerifyTransactions checks that the inputs to the transaction exist,
// that the transaction does not create or destroy coins and that the
// signatures on the transaction are valid
func (self *Blockchain) VerifyTransaction(tx *Transaction) error {
    //CHECKLIST: DONE: check for duplicate ux inputs/double spending
    //CHECKLIST: DONE: check that inputs of transaction have not been spent
    //CHECKLIST: DONE: check there are no duplicate outputs

    // Q: why are coin hours based on last block time and not
    // current time?
    // A: no two computers will agree on system time. Need system clock
    // indepedent timing that everyone agrees on. fee values would depend on
    // local clock

    // Verify the transaction's internals (hash check, surface checks)
    if err := tx.Verify(); err != nil {
        return err
    }
    uxIn, err := self.txUxIn(tx) //set of inputs referenced by transaction
    if err != nil {
        return err
    }
    //checks whether ux inputs exist, check signatures, checks for duplicate outputs
    if err := self.txUxInChk(tx, uxIn); err != nil {
        return err
    }
    uxOut := self.CreateExpectedOutputs(tx) // set of outputs created by transaction
    //checks for duplicate outputs, checks for hash collisions with unspent outputs
    if err := self.txUxOutChk(tx, uxOut); err != nil {
        return err
    }
    //checks coin balances and relationship between inputs and outputs
    if err := self.txUxChk(tx, uxIn, uxOut); err != nil {
        return err
    }
    return nil
}

// TransactionFee calculates the current transaction fee in coinhours of a transaction
func (self *Blockchain) TransactionFee(t *Transaction) (uint64, error) {
    var headTime uint64 = self.Time() //time of last block
    inHours := uint64(0)
    // Compute input hours
    for _, ti := range t.In {
        in, ok := self.Unspent.Get(ti.UxOut)
        if !ok {
            return 0, errors.New("TransactionFee(), error, unspent output does not exist")
        }
        inHours += in.CoinHours(headTime)
    }
    // Compute output hours
    outHours := uint64(0)
    for _, to := range t.Out {
        outHours += to.Hours
    }
    if inHours < outHours {
        return 0, errors.New("Overspending")
    }
    return inHours - outHours, nil
}

// Returns error if the BlockHeader is not valid
func (self *Blockchain) verifyBlockHeader(b *Block) error {
    //check BkSeq
    if b.Header.BkSeq != self.Head().Header.BkSeq+1 {
        return errors.New("BkSeq invalid")
    }
    //check Time, give some room for error and clock skew
    if b.Header.Time < self.Head().Header.Time+self.CreationInterval {
        return errors.New("time invalid: block too soon")
    }
    maxDiff := blockTimeFutureMultipleMax * self.CreationInterval
    if b.Header.Time > uint64(time.Now().UTC().Unix())+maxDiff {
        return errors.New("Block is too far in future; check clock")
    }

    // Check block sequence against previous head
    if b.Header.BkSeq != 0 && self.Head().Header.BkSeq+1 != b.Header.BkSeq {
        return errors.New("Header BkSeq not sequential")
    }
    // Check block hash against previous head
    if b.Header.PrevHash != self.Head().HashHeader() {
        return errors.New("HashPrevBlock does not match current head")
    }
    if b.HashBody() != b.Header.BodyHash {
        return errors.New("Body hash error hash error")
    }
    return nil
}

// Validates a set of Transactions, individually, against each other and
// against the Blockchain.  If firstFail is true, it will return an error
// as soon as it encounters one.  Else, it will return an array of
// Transactions that are valid as a whole.  It may return an error if
// firstFalse is false, if there is no way to filter the txns into a valid
// array, i.e. processTransactions(processTransactions(txn, false), true)
// should not result in an error, unless all txns are invalid.
func (self *Blockchain) processTransactions(txns Transactions,
    firstFail bool) (Transactions, error) {
    //TODO: audit
    // If there are no transactions, a block should not be made
    if len(txns) == 0 {
        if firstFail {
            return nil, errors.New("No transactions")
        } else {
            return txns, nil
        }
    }

    // Transactions must be sorted, so we can have deterministic filtering
    if !txns.IsSorted() {
        return nil, errors.New("Txns not sorted")
    }

    skip := make(map[int]byte)
    uxHashes := make(map[SHA256]byte, len(txns))
    for i, t := range txns {
        // Check the transaction against itself.  This covers the hash,
        // signature indices and duplicate spends within itself
        if err := self.VerifyTransaction(&t); err != nil {
            if firstFail {
                return nil, err
            } else {
                skip[i] = byte(1)
                continue
            }
        }
        // Check that each pending unspent will be unique
        uxb := UxBody{
            SrcTransaction: t.Header.Hash,
        }
        for _, to := range t.Out {
            uxb.Coins = to.Coins
            uxb.Hours = to.Hours
            uxb.Address = to.DestinationAddress
            h := uxb.Hash()
            _, exists := uxHashes[h]
            if exists {
                if firstFail {
                    m := "Duplicate unspent output across transactions"
                    return nil, errors.New(m)
                } else {
                    skip[i] = byte(1)
                    continue
                }
            }
            // Check that the expected unspent is not already in the pool
            // This should never happen
            if self.Unspent.Has(h) {
                if firstFail {
                    return nil, errors.New("Output hash is in the UnspentPool")
                } else {
                    skip[i] = byte(1)
                    continue
                }
            }
            uxHashes[h] = byte(1)
        }
    }

    // Filter invalid transactions before arbitrating between colliding ones
    if len(skip) > 0 {
        newtxns := make(Transactions, 0, len(txns)-len(skip))
        for i, txn := range txns {
            if _, shouldSkip := skip[i]; !shouldSkip {
                newtxns = append(newtxns, txn)
            }
        }
        txns = newtxns
        skip = make(map[int]byte)
    }

    // Check to ensure that there are no duplicate spends in the entire block,
    // and that we aren't creating duplicate outputs.  Duplicate outputs
    // within a single Transaction are already checked by VerifyTransaction
    for i := 0; i < len(txns)-1; i++ {
        s := txns[i]
        for j := i + 1; j < len(txns); j++ {
            t := txns[j]
            if s.Header.Hash == t.Header.Hash {
                // This is a non-recoverable error for filtering, and should
                // be considered a programming error
                return nil, errors.New("Duplicate transaction found")
            }
            for a := 0; a < len(s.In)-1; a++ {
                for b := a + 1; b < len(t.In); b++ {
                    if s.In[a].UxOut == t.In[b].UxOut {
                        if firstFail {
                            m := "Cannot spend output twice in the same block"
                            return nil, errors.New(m)
                        } else {
                            // The txn with the lowest hash is chosen when
                            // attempting a double spend. Since the txns
                            // are sorted, we skip the 2nd iterable
                            skip[j] = byte(1)
                        }
                    }
                }
            }
        }
    }

    // Filter the final results, if necessary
    if len(skip) > 0 {
        newtxns := make(Transactions, 0, len(txns)-len(skip))
        for i, txn := range txns {
            if _, shouldSkip := skip[i]; !shouldSkip {
                newtxns = append(newtxns, txn)
            }
        }
        return newtxns, nil
    } else {
        return txns, nil
    }
}

// Returns an error if any Transaction in txns is invalid
func (self *Blockchain) verifyTransactions(txns Transactions) error {
    _, err := self.processTransactions(txns, true)
    return err
}

// Returns an array of Transactions with invalid ones removed from txns.
// The Transaction hash is used to arbitrate between double spends.
// txns must be sorted by hash.
func (self *Blockchain) arbitrateTransactions(txns Transactions) Transactions {
    newtxns, err := self.processTransactions(txns, false)
    if err != nil {
        log.Panic("arbitrateTransactions failed unexpectedly: %v", err)
    }
    return newtxns
}

// Verifies the BlockHeader and BlockBody
func (self *Blockchain) VerifyBlock(b *Block) error {
    if err := self.verifyBlockHeader(b); err != nil {
        return err
    }
    if err := self.verifyTransactions(b.Body.Transactions); err != nil {
        return err
    }
    return nil
}

//ExecuteBlock attempts to append block to blockchain
func (self *Blockchain) ExecuteBlock(b Block) error {
    if err := self.VerifyBlock(&b); err != nil {
        return err
    }
    for _, tx := range b.Body.Transactions {
        // Remove spent outputs
        hashes := make([]SHA256, 0, len(tx.In))
        for _, ti := range tx.In {
            hashes = append(hashes, ti.UxOut)
        }
        self.Unspent.DelMultiple(hashes)
        // Create new outputs
        uxs := self.CreateOutputs(&tx, &b.Header)
        for _, ux := range uxs {
            self.Unspent.Add(ux)
        }
    }

    self.Blocks = append(self.Blocks, b)

    return nil
}

// Creates UxOut from TransactionInputs.  UxOut.Head() is not set here, use
func (self *Blockchain) CreateExpectedOutputs(tx *Transaction) UxArray {
    uxo := make(UxArray, 0, len(tx.Out))
    for _, to := range tx.Out {
        ux := UxOut{
            Body: UxBody{
                SrcTransaction: tx.Header.Hash,
                Address:        to.DestinationAddress,
                Coins:          to.Coins,
                Hours:          to.Hours,
            },
        }
        uxo = append(uxo, ux)
    }
    return uxo
}

// Creates complete UxOuts from TransactionInputs
// TODO: audit
func (self *Blockchain) CreateOutputs(tx *Transaction, bh *BlockHeader) UxArray {
    head := UxHead{
        Time:  bh.Time,
        BkSeq: bh.BkSeq,
    }
    uxo := self.CreateExpectedOutputs(tx)
    for i := 0; i < len(uxo); i++ {
        uxo[i].Head = head
    }
    return uxo
}
