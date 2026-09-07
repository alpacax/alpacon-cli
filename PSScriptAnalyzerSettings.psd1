@{
    # ParseError is a severity of its own: without it the analyzer reports
    # nothing at all for a file that does not parse.
    Severity = @('ParseError', 'Error', 'Warning')

    ExcludeRules = @(
        # An installer's whole job is to talk to the console. Write-Output would
        # put progress lines on the pipeline, where a caller reading the script's
        # output would have to filter them back out.
        'PSAvoidUsingWriteHost'

        # The Set- functions here are internal to this one script and are
        # never exported as cmdlets, so -WhatIf plumbing buys nothing.
        'PSUseShouldProcessForStateChangingFunctions'
    )
}
